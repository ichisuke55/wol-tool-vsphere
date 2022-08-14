package env

import (
	"io/ioutil"
	"log"

	yaml "gopkg.in/yaml.v2"
)

type EnvConfig struct {
	VcenterURL         string `split_words:"true" default:"https://vcsa" yaml:"vcenter_url"`
	AuthID             string `split_words:"true" yaml:"auth_id"`
	AuthPass           string `split_words:"true" yaml:"auth_pass"`
	SlackSigningSecret string `split_words:"true" yaml:"slack_signing_secret"`
	SlackToken         string `split_words:"true" yaml:"slack_token"`
	ESXiHosts          []Host `split_words:"true" yaml:"esxi_hosts"`
}

type Host struct {
	Name       string `split_words:"true" yaml:"name"`
	MacAddress string `split_words:"true" yaml:"mac"`
}

func NewEnvYaml() *EnvConfig {
	var c EnvConfig
	s, _ := ioutil.ReadFile("env.yaml")
	err := yaml.Unmarshal([]byte(s), &c)
	if err != nil {
		log.Fatal(err)
	}
	return &c

}
