package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25"

	"github.com/ichisuke55/wol-tool-vsphere/config"
)

type SessionResponse struct {
	Value string
}

func healthcheck(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "ok")
}

func findHosts(ctx context.Context, c *vim25.Client) ([]*object.HostSystem, error) {
	f := find.NewFinder(c)
	hss, err := f.HostSystemList(ctx, "*")
	if err != nil {
		return nil, err
	}

	return hss, nil
}

func main() {
	// Extract env config
	conf := config.NewConfig()

	// Set Slack API Token
	api := slack.New(conf.SlackToken)

	// Set context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// sessionUrl := "https://vcenter01.ichisuke.home/sdk"
	sessionUrl := conf.SessionURL
	u, err := url.Parse(sessionUrl)
	if err != nil {
		log.Fatalf("[ERROR] failed to parse url %v", err)
	}
	// Set BasicAuth
	u.User = url.UserPassword(conf.AuthID, conf.AuthPass)

	// Generate govmomi client
	c, err := govmomi.NewClient(ctx, u, true)
	if err != nil {
		log.Fatalf("[ERROR] failed to generate client %v", err)
	}

	http.HandleFunc("/slack/events", func(w http.ResponseWriter, r *http.Request) {
		verifier, err := slack.NewSecretsVerifier(r.Header, conf.SlackSigningSecret)
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
				message := strings.Split(event.Text, " ")
				if len(message) < 2 {
					w.WriteHeader(http.StatusBadRequest)
					return
				}

				command := message[1]
				switch command {
				case "shutdown":
					text := slack.NewTextBlockObject(slack.MarkdownType, "Please select *host*.", false, false)
					textSection := slack.NewSectionBlock(text, nil, nil)

					hss, err := findHosts(ctx, c.Client)
					if err != nil {
						log.Println(err)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					// Make hostnames slices
					var hostNames []string
					for _, v := range hss {
						hostSplits := strings.Split(v.Common.InventoryPath, "/")
						hn := hostSplits[len(hostSplits)-1]
						hostNames = append(hostNames, hn)
					}

					options := make([]*slack.OptionBlockObject, 0, len(hostNames))
					for _, v := range hostNames {
						optionText := slack.NewTextBlockObject(slack.PlainTextType, v, false, false)
						options = append(options, slack.NewOptionBlockObject(v, optionText, optionText))
					}

					placeholder := slack.NewTextBlockObject(slack.PlainTextType, "Select Host", false, false)
					selectMenu := slack.NewOptionsSelectBlockElement(slack.OptTypeStatic, placeholder, "", options...)
					actionBlock := slack.NewActionBlock("select-host-action", selectMenu)

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

	})

	http.HandleFunc("/health", healthcheck)

	log.Println("[INFO] Server listening...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}

	// var targetHS *object.HostSystem
	// for _, v := range hs {
	// 	// Extract targetHost
	// 	if strings.Contains(v.Common.InventoryPath, "esxi03") {
	// 		targetHS = v
	// 	}
	// }

	// // Make request host shutdown
	// req := types.ShutdownHost_Task{
	// 	This:  targetHS.Common.Reference(),
	// 	Force: true,
	// }

	// // Send shutdown request
	// _, err = methods.ShutdownHost_Task(ctx, targetHS.Common.Client(), &req)
	// if err != nil {
	// 	log.Fatalf("[ERROR] failed to shutdown request %v", err)
	// }

}
