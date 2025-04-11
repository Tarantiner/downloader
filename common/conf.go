package common

import (
	"github.com/gotd/td/telegram"
	"gopkg.in/ini.v1"
	"strings"
)

type Config struct {
	Download struct {
		APIID         int    `ini:"apiID"`
		APIHash       string `ini:"apiHash"`
		SessionDir    string `ini:"sessionDir"`
		DataDir       string `ini:"dataDir"`
		Threads       int    `ini:"threads"`
		DownloadTypes string `ini:"downloadTypes"`
		Dtypes        map[string]struct{}
	} `ini:"download"`

	Login struct {
		APIID      int    `ini:"apiID"`
		APIHash    string `ini:"apiHash"`
		SessionDir string `ini:"sessionDir"`
	} `ini:"login"`

	NET struct {
		UseProxy  bool   `ini:"useProxy"`
		ProxyHost string `ini:"proxyHost"`
		ProxyPort int    `ini:"proxyPort"`
	} `ini:"net"`

	DB struct {
		DBPath string `ini:"dbPath"`
	} `ini:"database"`

	Common struct {
		LogSplitSize int `ini:"logSplitSize"`
	}
}

func LoadConfig(config *Config, path string) error {
	err := ini.MapTo(config, path)
	if err != nil {
		return err
	}

	// search
	if config.Download.APIID == -1 {
		config.Download.APIID = telegram.TestAppID
	}
	if config.Download.APIHash == "" {
		config.Download.APIHash = telegram.TestAppHash
	}
	if config.Download.Threads == -1 {
		config.Download.Threads = 4
	}
	lis := strings.Split(config.Download.DownloadTypes, ",")
	mp := make(map[string]struct{})
	for _, v := range lis {
		if v == "" {
			continue
		}
		mp[v] = struct{}{}
	}
	config.Download.Dtypes = mp

	// login
	if config.Login.APIID == -1 {
		config.Login.APIID = telegram.TestAppID
	}
	if config.Login.APIHash == "" {
		config.Login.APIHash = telegram.TestAppHash
	}

	// common
	if config.Common.LogSplitSize == -1 {
		config.Common.LogSplitSize = 2
	}
	return nil
}
