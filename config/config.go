package config

import (
	"github.com/kelseyhightower/envconfig"
)

type EnvConfig struct {
	SessionURL         string `split_words:"true" default:"https://vcsa/sdk"`
	AuthID             string `split_words:"true"`
	AuthPass           string `split_words:"true"`
	SlackSigningSecret string `split_words:"true"`
	SlackToken         string `split_words:"true"`
}

func NewConfig() *EnvConfig {
	var conf EnvConfig
	envconfig.Process("", &conf)
	return &conf
}
