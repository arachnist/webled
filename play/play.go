package play

import (
	"container/list"
	"sync"

	"github.com/golang/glog"
	"golang.org/x/net/context"
	"google.golang.org/grpc"

	pb "github.com/q3k/webled/proto"
)

type VideoMeta struct {
	Title string
	File  string
}

type PlaylistCommand struct {
	command int
	title   string
	file    string
}

const (
	PLAYLIST_RESUME = iota
	PLAYLIST_STOP   = iota
	PLAYLIST_CLEAR  = iota

	PLAYLIST_NOW    = iota
	PLAYLIST_APPEND = iota
	PLAYLIST_INSERT = iota
)

type Player struct {
	client   pb.RemoteVideoClient
	playlist struct {
		events   chan bool
		curate   bool
		mutex    sync.RWMutex
		videos   *list.List
		index    int
		commands chan PlaylistCommand
	}
}

func NewPlayer(remote string) (*Player, error) {
	conn, err := grpc.Dial(remote, grpc.WithInsecure())
	if err != nil {
		return nil, err
	}
	p := &Player{
		client: pb.NewRemoteVideoClient(conn),
	}
	p.playlist.events = make(chan bool, 100)
	p.playlist.videos = list.New()
	p.playlist.commands = make(chan PlaylistCommand, 100)
	p.playlist.curate = false
	return p, nil
}

func (p *Player) Now() *VideoMeta {
	p.playlist.mutex.RLock()
	defer p.playlist.mutex.RUnlock()
	if p.playlist.index < 0 || p.playlist.index >= p.playlist.videos.Len() {
		return nil
	}
	elem := p.playlist.videos.Front()
	for i := 0; i < p.playlist.index; i++ {
		elem = elem.Next()
	}
	meta := elem.Value.(VideoMeta)
	return &meta
}

func (p *Player) curator() {
	for {
		<-p.playlist.events
		p.playlist.mutex.Lock()
		if !p.playlist.curate {
			p.client.Interrupt(context.Background(), &pb.InterruptRequest{})
			p.playlist.mutex.Unlock()
			continue
		}
		// Clamp the current cursor to the playlist
		if p.playlist.index < 0 {
			p.playlist.index = 0
		}

		// Is there a video to play?
		i := 0
		for e := p.playlist.videos.Front(); e != nil; e = e.Next() {
			if i == p.playlist.index {
				meta := e.Value.(VideoMeta)
				req := &pb.PlayRequest{
					Filename: meta.File,
				}
				go func() {
					glog.Infof("Now playing: %v (%v)", meta.Title, meta.File)
					_, err := p.client.Play(context.Background(), req)
					if err != nil {
						glog.Infof("Playback result: %v", err)
						return
					}
					glog.Info("Finished playback, continuing with playlist...")
					p.playlist.mutex.Lock()
					defer p.playlist.mutex.Unlock()
					p.playlist.index += 1
					p.playlist.events <- true
				}()
			}
			i += 1
		}

		p.playlist.mutex.Unlock()
	}
}

func (p *Player) StartPlaylist() {
	go func() {
		for {
			command := <-p.playlist.commands
			glog.Infof("Playlist got command: %v", command)
			p.playlist.mutex.Lock()
			switch command.command {
			case PLAYLIST_RESUME:
				// Kick the curator
				p.playlist.curate = true
				p.playlist.events <- true
			case PLAYLIST_STOP:
				// Disable the curator
				p.playlist.curate = false
				p.playlist.events <- true
			case PLAYLIST_CLEAR:
				for e := p.playlist.videos.Front(); e != nil; e = e.Next() {
					p.playlist.videos.Remove(e)
				}
				p.playlist.events <- true
			case PLAYLIST_NOW:
				meta := VideoMeta{
					Title: command.title,
					File:  command.file,
				}
				p.playlist.videos = list.New()
				p.playlist.videos.PushBack(meta)
				p.playlist.index = 0
				p.playlist.curate = true
				p.playlist.events <- true
			case PLAYLIST_APPEND:
				meta := VideoMeta{
					Title: command.title,
					File:  command.file,
				}
				p.playlist.videos.PushBack(meta)
			}
			p.playlist.mutex.Unlock()
		}
	}()
	go p.curator()
	p.client.Interrupt(context.Background(), &pb.InterruptRequest{})
}

func (p *Player) GetPlaylist() []VideoMeta {
	p.playlist.mutex.RLock()
	defer p.playlist.mutex.RUnlock()
	l := []VideoMeta{}
	for e := p.playlist.videos.Front(); e != nil; e = e.Next() {
		l = append(l, e.Value.(VideoMeta))
	}
	return l
}

func (p *Player) PlayNow(title string, file string) {
	c := PlaylistCommand{
		command: PLAYLIST_NOW,
		title:   title,
		file:    file,
	}
	p.playlist.commands <- c
	p.Resume()
}

func (p *Player) PlayAppend(title string, file string) {
	c := PlaylistCommand{
		command: PLAYLIST_APPEND,
		title:   title,
		file:    file,
	}
	p.playlist.commands <- c
}

func (p *Player) Append(title string, file string) {
	c := PlaylistCommand{
		command: PLAYLIST_APPEND,
		title:   title,
		file:    file,
	}
	p.playlist.commands <- c
}

func (p *Player) Resume() {
	c := PlaylistCommand{
		command: PLAYLIST_RESUME,
	}
	p.playlist.commands <- c
}

func (p *Player) Stop() {
	c := PlaylistCommand{
		command: PLAYLIST_STOP,
	}
	p.playlist.commands <- c
}
