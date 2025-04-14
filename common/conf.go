package common

import (
	"github.com/gotd/td/telegram"
	"gopkg.in/ini.v1"
	"strings"
)

type Config struct {
	Download struct {
		APIID                int    `ini:"apiID"`
		APIHash              string `ini:"apiHash"`
		SessionDir           string `ini:"sessionDir"`
		DataDir              string `ini:"dataDir"`
		Threads              int    `ini:"threads"` // 此版本不支持多线程，为无用参数
		DownloadTypes        string `ini:"downloadTypes"`
		ExcludeDownloadTypes string `ini:"excludeDownloadTypes"`
		Dtypes               map[string]struct{}
		EDtypes              map[string]struct{}
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
	lis2 := strings.Split(config.Download.ExcludeDownloadTypes, ",")
	mp1 := make(map[string]struct{})
	for _, v := range lis {
		if v == "" {
			continue
		}
		mp1[v] = struct{}{}
	}
	config.Download.Dtypes = mp1

	mp2 := make(map[string]struct{})
	for _, v := range lis2 {
		if v == "" {
			continue
		}
		mp2[v] = struct{}{}
	}
	config.Download.EDtypes = mp2

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
