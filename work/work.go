package work

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/jpillora/backoff"
	"golang.org/x/net/context"
	"golang.org/x/net/trace"
)

const (
	kMaxWorkers      = 1024
	kWorkQueueLength = 200
)

type WorkType int

const (
	WORK_REMOVE_FILE = iota
	WORK_WEBDOWNLOAD = iota
	WORK_CONVERT     = iota
)

type WorkRequest struct {
	Type        WorkType
	Context     context.Context
	Depends     *WorkRequest
	Done        bool
	Backoff     backoff.Backoff
	WebDownload struct {
		VideoURL   *url.URL
		TargetPath string
	}
	RemoveFile struct {
		Path string
	}
	Convert struct {
		SourcePath string
		TargetPath string
	}
}

type workerQueue chan chan *WorkRequest

type Worker struct {
	ID          int
	Work        chan *WorkRequest
	WorkerQueue workerQueue
	QuitChan    chan bool
	Current     *WorkRequest
}

func (w *Worker) handleWebDownload() error {
	work := w.Current
	tmpTargetPath := fmt.Sprintf("%s.temporary", work.WebDownload.TargetPath)
	execCmd := fmt.Sprintf("mv {} %s", work.WebDownload.TargetPath)
	args := []string{
		work.WebDownload.VideoURL.String(),
		"--max-downloads=1",
		"-o", tmpTargetPath,
		"--exec", execCmd,
	}
	if tr, ok := trace.FromContext(work.Context); ok {
		tr.LazyPrintf("Starting download %v...", args)
	}
	cmd := exec.Command("youtube-dl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}
	if tr, ok := trace.FromContext(work.Context); ok {
		tr.LazyPrintf("Command output: %v", string(out))
	}
	if !cmd.ProcessState.Success() {
		errors.New("Process exited with non-zero status.")
	}
	return nil
}

func (w *Worker) handleRemoveFile() error {
	work := w.Current
	if tr, ok := trace.FromContext(work.Context); ok {
		tr.LazyPrintf("Removing file %s...", work.RemoveFile.Path)
	}
	os.Remove(work.RemoveFile.Path)
	return nil
}

func (w *Worker) handleConvert() error {
	work := w.Current
	args := []string{
		"-i", work.Convert.SourcePath,
		"-vf", "scale=-2:128,crop=128:128",
		"-c:v", "libvpx", "-b:v", "1M",
		"-c:a", "libvorbis",
		work.Convert.TargetPath,
	}
	if tr, ok := trace.FromContext(work.Context); ok {
		tr.LazyPrintf("Starting ffmpeg %v...", args)
	}
	cmd := exec.Command("ffmpeg", args...)
	out, err := cmd.CombinedOutput()
	if tr, ok := trace.FromContext(work.Context); ok {
		tr.LazyPrintf("Command output: %v", string(out))
	}
	if err != nil {
		return err
	}
	return nil
}

func (w *Worker) Start() {
	go func() {
		for {
			w.WorkerQueue <- w.Work
			select {
			case work := <-w.Work:
				if tr, ok := trace.FromContext(work.Context); ok {
					tr.LazyPrintf("Hit worker %d", w.ID)
				}
				w.Current = work
				var handler func() error
				switch work.Type {
				case WORK_WEBDOWNLOAD:
					handler = w.handleWebDownload
				case WORK_REMOVE_FILE:
					handler = w.handleRemoveFile
				case WORK_CONVERT:
					handler = w.handleConvert
				default:
					handler = func() error { return nil }
				}
				if err := handler(); err != nil {
					glog.Error("Error running handler: %v", err)
					if tr, ok := trace.FromContext(work.Context); ok {
						tr.LazyPrintf("Error running handler: %v", err)
						tr.SetError()
						tr.Finish()
					}
				} else {
					if tr, ok := trace.FromContext(work.Context); ok {
						tr.LazyPrintf("Done.")
						tr.Finish()
					}
				}
				work.Done = true
			}
			w.Current = nil
		}
	}()
}

type Overlord struct {
	workQueue   chan *WorkRequest
	freeWorkers workerQueue
	allWorkers  []*Worker
	mutex       sync.RWMutex
}

func NewOverlord() Overlord {
	o := Overlord{
		freeWorkers: make(chan chan *WorkRequest, kMaxWorkers),
		allWorkers:  []*Worker{},
		workQueue:   make(chan *WorkRequest, kWorkQueueLength),
	}
	return o
}

func NewRequest(ctx context.Context, t WorkType, f string, args ...interface{}) *WorkRequest {
	n := fmt.Sprintf(f, args...)
	r := &WorkRequest{
		Type:    t,
		Context: trace.NewContext(ctx, trace.New("webled.work", n)),
	}
	return r
}

func (o *Overlord) WebDownload(ctx context.Context, uri string, target string) error {
	uriParsed, err := url.Parse(uri)
	if err != nil {
		return err
	}
	tmpFileWeb, err := ioutil.TempFile("", "ledweb")
	if err != nil {
		return err
	}
	tmpWeb := tmpFileWeb.Name()

	dlreq := NewRequest(ctx, WORK_WEBDOWNLOAD, "ytdownload(%s, %s)", uriParsed.String(), tmpWeb)
	dlreq.WebDownload.VideoURL = uriParsed
	dlreq.WebDownload.TargetPath = tmpWeb
	o.workQueue <- dlreq

	convertReq := NewRequest(ctx, WORK_CONVERT, "convert(%s, %s)", tmpWeb, target)
	convertReq.Convert.SourcePath = tmpWeb
	convertReq.Convert.TargetPath = target
	convertReq.Depends = dlreq
	o.workQueue <- convertReq

	rmWebReq := NewRequest(ctx, WORK_REMOVE_FILE, "remove_file(%s)", tmpWeb)
	rmWebReq.RemoveFile.Path = tmpWeb
	rmWebReq.Depends = convertReq
	o.workQueue <- rmWebReq

	return nil
}

func (o *Overlord) SpawnWorker() {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	newID := len(o.allWorkers)
	w := Worker{
		ID:          newID,
		Work:        make(chan *WorkRequest, kWorkQueueLength),
		WorkerQueue: o.freeWorkers,
		QuitChan:    make(chan bool, 1),
	}
	o.allWorkers = append(o.allWorkers, &w)
	w.Start()
}

func (o *Overlord) StartDispatching() {
	go func() {
		for {
			select {
			case work := <-o.workQueue:
				if work.Depends != nil && !work.Depends.Done {
					if tr, ok := trace.FromContext(work.Context); ok {
						tr.LazyPrintf("Blocked on precondition...")
					}
					go func(w *WorkRequest) {
						time.Sleep(w.Backoff.Duration())
						o.workQueue <- w
					}(work)
					continue
				}
				if tr, ok := trace.FromContext(work.Context); ok {
					tr.LazyPrintf("Dispatching...")
				}
				go func(w *WorkRequest) {
					worker := <-o.freeWorkers
					worker <- w
				}(work)
			}
		}
	}()
}

func (o *Overlord) GetWorkers() []Worker {
	o.mutex.RLock()
	defer o.mutex.RUnlock()
	workers := []Worker{}
	for _, worker := range o.allWorkers {
		workers = append(workers, *worker)
	}
	return workers
}
