package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/golang/glog"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
)

const (
	kVideoMetaDir = "/var/webled/meta"
	kVideoDataDir = "/var/webled/data"
)

type WebMeta struct {
	FullTitle string `json:"fulltitle"`
	ID        string `json:"id"`
}

func getWebMeta(uri string) ([]byte, *WebMeta, error) {
	cmd := exec.Command("youtube-dl", "-j", uri)
	out, err := cmd.Output()
	if err != nil {
		return nil, nil, err
	}
	meta := &WebMeta{}
	err = json.NewDecoder(strings.NewReader(string(out))).Decode(meta)
	if err != nil {
		return nil, nil, err
	}
	return out, meta, nil
}

type LibraryEntry struct {
	Title     string
	File      string
	ID        string
	SourceURL string
}

type Librarian struct {
}

type Callback func(string, string)

func (l *Librarian) Start() error {
	if err := os.Mkdir(kVideoMetaDir, 0755); err != nil {
		return err
	}
	if err := os.Mkdir(kVideoDataDir, 0755); err != nil {
		return err
	}
	return nil
}

func (l *Librarian) AcquireAndPlay(ctx context.Context, uri string, c Callback) ([]int64, error) {
	metaBytes, meta, err := getWebMeta(uri)
	if err != nil {
		return []int64{}, errors.New(fmt.Sprintf("Invalid URI (%s: %v).", uri, err))
	}
	id := meta.ID
	glog.Info("Getting video %s...", id)
	metaFile := fmt.Sprintf("%s/%s.json", kVideoMetaDir, id)
	dataFile := fmt.Sprintf("%s/%s.webm", kVideoDataDir, id)
	_, err = os.Stat(metaFile)

	if err != nil {
		glog.Infof("Video %s not present, downloading.", uri)
		uids, err := overlord.WebDownload(ctx, uri, dataFile)
		if err != nil {
			return []int64{}, err
		}
		// Wait for the video to start being converted, then save meta and
		// trigger callback.
		go func(title string, path string) {
			for {
				stat, err := os.Stat(path)
				if err != nil {
					continue
				}
				if stat.Size() < 1024*1024 {
					continue
				}
				ioutil.WriteFile(metaFile, metaBytes, 0644)
				go c(title, path)
				break
			}
		}(meta.FullTitle, dataFile)
		return uids, nil
	} else {
		glog.Infof("Video %s present.", uri)
		data, err := ioutil.ReadFile(metaFile)
		if err != nil {
			return []int64{}, err
		}
		meta := &WebMeta{}
		err = json.NewDecoder(strings.NewReader(string(data))).Decode(meta)
		if err != nil {
			return []int64{}, err
		}
		go c(meta.FullTitle, dataFile)
		return []int64{}, nil
	}
}

func (l *Librarian) GetVideos(ctx context.Context) ([]LibraryEntry, error) {
	flist, err := ioutil.ReadDir(kVideoMetaDir)
	if err != nil {
		return nil, err
	}
	metaList := []LibraryEntry{}
	for _, info := range flist {
		metaName := fmt.Sprintf("%s/%s", kVideoMetaDir, info.Name())
		data, err := ioutil.ReadFile(metaName)
		if err != nil {
			return nil, err
		}
		ytMeta := &WebMeta{}
		err = json.NewDecoder(strings.NewReader(string(data))).Decode(ytMeta)
		if err != nil {
			return nil, err
		}
		dataName := fmt.Sprintf("%s/%s.webm", kVideoDataDir, ytMeta.ID)
		meta := LibraryEntry{
			Title: ytMeta.FullTitle,
			File:  dataName,
			ID:    ytMeta.ID,
		}
		metaList = append(metaList, meta)
	}
	return metaList, nil
}
