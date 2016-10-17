package main

import (
	"errors"
	"flag"
	"html/template"
	"net/http"

	"github.com/golang/glog"
	"golang.org/x/net/context"
	"golang.org/x/net/trace"

	"github.com/q3k/webled/play"
	"github.com/q3k/webled/work"
)

var (
	bindAddress   string
	remoteAddress string
	overlord      work.Overlord
	player        *play.Player
	librarian     Librarian
)

type pageStatus struct {
	NowPlaying *play.VideoMeta
	Overlord   *work.Overlord
	Workers    []work.Worker
	Playlist   []play.VideoMeta
	Library    []LibraryEntry
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	t, err := template.ParseFiles("templates/status.html")
	if err != nil {
		w.WriteHeader(500)
		glog.Error(err)
		return
	}
	videos, err := librarian.GetVideos(ctx)
	if err != nil {
		w.WriteHeader(500)
		glog.Error(err)
		return
	}
	p := pageStatus{
		Overlord:   &overlord,
		Workers:    overlord.GetWorkers(),
		Playlist:   player.GetPlaylist(),
		Library:    videos,
		NowPlaying: player.Now(),
	}
	t.Execute(w, p)
}

type APIPlaylist struct {
	Videos []play.VideoMeta `json:"videos"`
}

type APILibrary struct {
	Videos []LibraryEntry `json:"videos"`
}

func apiPlay(ctx context.Context, r *http.Request, c func(string, string)) error {
	uri := r.URL.Query().Get("uri")
	id := r.URL.Query().Get("id")
	if uri == "" && id == "" {
		return errors.New("No uri or id provided.")
	}
	if uri != "" {
		err := librarian.AcquireAndPlay(ctx, uri, c)
		if err != nil {
			return err
		}
	} else {
		videos, err := librarian.GetVideos(ctx)
		if err != nil {
			return err
		}
		for _, video := range videos {
			if video.ID == id {
				c(video.Title, video.File)
				break
			}
		}
	}
	return nil
}

func main() {
	flag.StringVar(&remoteAddress, "remote_address", "127.0.0.1:8080", "Address of the remote GRPC endpoint.")
	flag.StringVar(&bindAddress, "bind_address", ":8081", "Address to bind web interface to.")
	flag.Parse()
	glog.Info("Starting webled...")

	overlord = work.NewOverlord()
	overlord.SpawnWorker()
	overlord.SpawnWorker()
	overlord.SpawnWorker()
	overlord.SpawnWorker()
	overlord.StartDispatching()

	librarian.Start()

	var err error
	player, err = play.NewPlayer(remoteAddress)
	if err != nil {
		glog.Exit(err)
	}
	player.StartPlaylist()

	http.HandleFunc("/", handleStatus)

	handleAPI("webled/library/get", func(ctx context.Context, r *http.Request) (interface{}, error) {
		videos, err := librarian.GetVideos(ctx)
		if err != nil {
			return nil, err
		}
		a := APILibrary{Videos: videos}
		return a, nil
	})

	handleAPI("webled/playlist/get", func(ctx context.Context, r *http.Request) (interface{}, error) {
		videos := player.GetPlaylist()
		a := APIPlaylist{Videos: videos}
		return a, nil
	})

	handleAPI("webled/playlist/play/now", func(ctx context.Context, r *http.Request) (interface{}, error) {
		return nil, apiPlay(ctx, r, player.PlayNow)
	})

	handleAPI("webled/playlist/play/append", func(ctx context.Context, r *http.Request) (interface{}, error) {
		return nil, apiPlay(ctx, r, player.PlayAppend)
	})

	handleAPI("webled/playlist/stop", func(ctx context.Context, r *http.Request) (interface{}, error) {
		player.Stop()
		return nil, nil
	})

	http.HandleFunc("/requests", func(w http.ResponseWriter, r *http.Request) {
		trace.Render(w, r, true)
	})
	glog.Error(http.ListenAndServe(bindAddress, nil))
}
