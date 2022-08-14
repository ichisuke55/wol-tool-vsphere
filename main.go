package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/ichisuke55/wol-tool-vsphere/env"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/types"
)

type SessionResponse struct {
	Value string
}

const (
	selectShutdownHostAction = "select-shutdown-host-action"
	selectBootHostAction     = "select-boot-host-action"
	confirmShutdownAction    = "confirm-shutdown-aciton"
	confirmBootAction        = "confirm-boot-aciton"
)

func findHosts(ctx context.Context, c *vim25.Client) ([]*object.HostSystem, error) {
	f := find.NewFinder(c)
	hss, err := f.HostSystemList(ctx, "*")
	if err != nil {
		return nil, err
	}
	return hss, nil
}

func provideButton(placeholder string) (*slack.ButtonBlockElement, *slack.ButtonBlockElement) {
	// Make confirm exec button
	confirmButtonText := slack.NewTextBlockObject(slack.PlainTextType, "Confirm", false, false)
	confirmButton := slack.NewButtonBlockElement("", placeholder, confirmButtonText)
	confirmButton.WithStyle(slack.StylePrimary)

	// Make cancel exec button
	cancelButtonText := slack.NewTextBlockObject(slack.PlainTextType, "Cancel", false, false)
	cancelButton := slack.NewButtonBlockElement("", placeholder, cancelButtonText)
	cancelButton.WithStyle(slack.StyleDanger)

	return confirmButton, cancelButton
}

func slackVerificationMiddleware(next http.HandlerFunc, signSecret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		verifier, err := slack.NewSecretsVerifier(r.Header, signSecret)
		if err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		bodyReader := io.TeeReader(r.Body, &verifier)
		body, err := ioutil.ReadAll(bodyReader)
		if err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if err := verifier.Ensure(); err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		r.Body = ioutil.NopCloser(bytes.NewBuffer(body))
		next.ServeHTTP(w, r)
	}
}

func wakeOnLan(mac net.HardwareAddr) {
	remoteUdpAddr, _ := net.ResolveUDPAddr("udp", "255.255.255.255:9")
	localUdpAddr, _ := net.ResolveUDPAddr("udp", ":0")
	conn, err := net.DialUDP("udp", localUdpAddr, remoteUdpAddr)
	if err != nil {
		log.Println(err)
		return
	}
	defer func() {
		_ = conn.Close()
	}()

	packetPrefix := make([]byte, 6)
	for i := range packetPrefix {
		packetPrefix[i] = 0xFF
	}

	sendPacket := make([]byte, 0)
	sendPacket = append(sendPacket, packetPrefix...)
	for i := 0; i < 16; i++ {
		sendPacket = append(sendPacket, mac...)
	}

	_, err = conn.Write(sendPacket)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
}

func main() {
	// Extract env config
	conf := env.NewEnvYaml()

	// Set Slack API Token
	api := slack.New(conf.SlackToken)

	// Set context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sessionUrl := fmt.Sprintf("%s/sdk", conf.VcenterURL)
	u, err := url.Parse(sessionUrl)
	if err != nil {
		log.Fatalf("[ERROR] failed to parse url %v", err)
	}
	// Set BasicAuth
	u.User = url.UserPassword(conf.AuthID, conf.AuthPass)

	// Generate govmomi client and verify access
	c, err := govmomi.NewClient(ctx, u, true)
	if err != nil {
		log.Fatalf("[ERROR] failed to generate client %v", err)
	}

	// Slack event handler
	http.HandleFunc("/slack/events", slackVerificationMiddleware(func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		eventsAPIEvent, err := slackevents.ParseEvent(json.RawMessage(body), slackevents.OptionNoVerifyToken())
		if err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		switch eventsAPIEvent.Type {
		case slackevents.URLVerification:
			var res *slackevents.ChallengeResponse
			if err := json.Unmarshal(body, &res); err != nil {
				log.Println(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			if _, err := w.Write([]byte(res.Challenge)); err != nil {
				log.Println(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

		case slackevents.CallbackEvent:
			innerEvent := eventsAPIEvent.InnerEvent
			switch event := innerEvent.Data.(type) {
			case *slackevents.AppMentionEvent:
				// fixme future
				message := strings.Split(event.Text, " ")
				if len(message) < 2 {
					w.WriteHeader(http.StatusBadRequest)
					return
				}

				command := message[1]
				switch command {
				case "shutdown":
					text := slack.NewTextBlockObject(slack.MarkdownType, "Please select *shutdown ESXi host*.", false, false)
					textSection := slack.NewSectionBlock(text, nil, nil)

					hss, err := findHosts(ctx, c.Client)
					if err != nil {
						log.Println(err)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}

					// Make hostnames slices & select box slices
					var hostNames []string
					options := make([]*slack.OptionBlockObject, 0, len(hostNames))
					for _, v := range hss {
						hostSplits := strings.Split(v.Common.InventoryPath, "/")
						hn := hostSplits[len(hostSplits)-1]
						hostNames = append(hostNames, hn) // fixme

						optionText := slack.NewTextBlockObject(slack.PlainTextType, hn, false, false)
						options = append(options, slack.NewOptionBlockObject(hn, optionText, optionText))
					}

					placeholder := slack.NewTextBlockObject(slack.PlainTextType, "Select Host", false, false)
					selectMenu := slack.NewOptionsSelectBlockElement(slack.OptTypeStatic, placeholder, "", options...)
					actionBlock := slack.NewActionBlock(selectShutdownHostAction, selectMenu)

					fallbackText := slack.MsgOptionText("This client is not supported.", false)
					blocks := slack.MsgOptionBlocks(textSection, actionBlock)

					if _, err := api.PostEphemeral(event.Channel, event.User, fallbackText, blocks); err != nil {
						log.Println(err)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}

				case "boot":
					text := slack.NewTextBlockObject(slack.MarkdownType, "Please select *boot ESXi host*.", false, false)
					textSection := slack.NewSectionBlock(text, nil, nil)

					hss, err := findHosts(ctx, c.Client)
					if err != nil {
						log.Println(err)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}

					// Make hostnames slices & select box slices
					var hostNames []string
					options := make([]*slack.OptionBlockObject, 0, len(hostNames))
					for _, v := range hss {
						hostSplits := strings.Split(v.Common.InventoryPath, "/")
						hn := hostSplits[len(hostSplits)-1]
						hostNames = append(hostNames, hn) // fixme

						optionText := slack.NewTextBlockObject(slack.PlainTextType, hn, false, false)
						options = append(options, slack.NewOptionBlockObject(hn, optionText, optionText))
					}

					placeholder := slack.NewTextBlockObject(slack.PlainTextType, "Select Host", false, false)
					selectMenu := slack.NewOptionsSelectBlockElement(slack.OptTypeStatic, placeholder, "", options...)
					actionBlock := slack.NewActionBlock(selectBootHostAction, selectMenu)

					fallbackText := slack.MsgOptionText("This client is not supported.", false)
					blocks := slack.MsgOptionBlocks(textSection, actionBlock)

					if _, err := api.PostEphemeral(event.Channel, event.User, fallbackText, blocks); err != nil {
						log.Println(err)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
				}
			}
		}

	}, conf.SlackSigningSecret))

	// Interaction action
	http.HandleFunc("/slack/actions", slackVerificationMiddleware(func(w http.ResponseWriter, r *http.Request) {
		var payload *slack.InteractionCallback
		if err := json.Unmarshal([]byte(r.FormValue("payload")), &payload); err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		switch payload.Type {
		case slack.InteractionTypeBlockActions:
			if len(payload.ActionCallback.BlockActions) == 0 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			action := payload.ActionCallback.BlockActions[0]
			switch action.BlockID {
			case selectShutdownHostAction:
				targetHost := action.SelectedOption.Value

				text := slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("Do you shutdown *%s*?", targetHost), false, false)
				textSection := slack.NewSectionBlock(text, nil, nil)

				// Generate button
				confirmButton, cancelButton := provideButton(targetHost)
				actionBlock := slack.NewActionBlock(confirmShutdownAction, confirmButton, cancelButton)

				fallbackText := slack.MsgOptionText("This client is not supported", false)
				blocks := slack.MsgOptionBlocks(textSection, actionBlock)

				replaceOriginal := slack.MsgOptionReplaceOriginal(payload.ResponseURL)
				if _, _, _, err := api.SendMessage("", replaceOriginal, fallbackText, blocks); err != nil {
					log.Println(err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			case selectBootHostAction:
				targetHost := action.SelectedOption.Value

				text := slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("Do you boot *%s*?", targetHost), false, false)
				textSection := slack.NewSectionBlock(text, nil, nil)

				// Generate button
				confirmButton, cancelButton := provideButton(targetHost)
				actionBlock := slack.NewActionBlock(confirmBootAction, confirmButton, cancelButton)

				fallbackText := slack.MsgOptionText("This client is not supported", false)
				blocks := slack.MsgOptionBlocks(textSection, actionBlock)

				replaceOriginal := slack.MsgOptionReplaceOriginal(payload.ResponseURL)
				if _, _, _, err := api.SendMessage("", replaceOriginal, fallbackText, blocks); err != nil {
					log.Println(err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			case confirmShutdownAction:
				go func() {
					hss, err := findHosts(ctx, c.Client)
					if err != nil {
						log.Println(err)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}

					var targetHS *object.HostSystem
					for _, v := range hss {
						if strings.Contains(v.Common.InventoryPath, action.Value) {
							targetHS = v
						}
					}

					req := types.ShutdownHost_Task{
						This:  targetHS.Common.Reference(),
						Force: true,
					}

					// Send shutdown request
					_, err = methods.ShutdownHost_Task(ctx, targetHS.Common.Client(), &req)
					if err != nil {
						log.Println(err)
					}
				}()

				deleteOriginal := slack.MsgOptionDeleteOriginal(payload.ResponseURL)
				if _, _, _, err := api.SendMessage("", deleteOriginal); err != nil {
					log.Println(err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			case confirmBootAction:
				go func() {
					for _, v := range conf.ESXiHosts {
						if action.Value == v.Name {
							hwmac, err := net.ParseMAC(v.MacAddress)
							if err != nil {
								log.Println(err)
							}
							wakeOnLan(hwmac)
						}
					}

				}()

				deleteOriginal := slack.MsgOptionDeleteOriginal(payload.ResponseURL)
				if _, _, _, err := api.SendMessage("", deleteOriginal); err != nil {
					log.Println(err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			}

		}

	}, conf.SlackSigningSecret))

	// Health check
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ok")
	})

	log.Println("[INFO] Server listening...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}

}
