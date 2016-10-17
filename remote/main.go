package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/golang/glog"
	"golang.org/x/net/context"
	"golang.org/x/net/trace"
	"google.golang.org/grpc"

	pb "github.com/q3k/webled/proto"
)

var (
	mpvArgs = []string{
		"#FNAME#",
		"--input-unix-socket", "#SNAME#",
		"-vo", "led",
		"-ao", "pulse:10.8.1.16",
	}
	process       *os.Process
	processMutex  sync.Mutex
	mpvSocketName string
	mpvSocket     net.Conn
	bindPort      int
)

func blank(ctx context.Context) {
	//TODO(q3k): implement this
}

func Stop(ctx context.Context) {
	processMutex.Lock()
	defer processMutex.Unlock()
	stop(ctx)
}

func stop(ctx context.Context) {
	if process == nil {
		return
	}
	glog.Info("Stopping previous process...")
	if tr, ok := trace.FromContext(ctx); ok {
		tr.LazyPrintf("Stopping previous process...")
	}
	process.Kill()
	blank(ctx)
}

// See JSON IPC in mpv(1)
type MPVCommand struct {
	Command []string `json:"command"`
}

type MPVResponse struct {
	Error string `json:"error"`
	Data  string `json:"data"`
}

func mpvReconnect(ctx context.Context) {
	for {
		if mpvSocket != nil {
			return
		}
		glog.Infof("Connecting to %s...", mpvSocketName)
		s, err := net.Dial("unix", mpvSocketName)
		if err == nil {
			mpvSocket = s
			glog.Info("Connected!")
			return
		}
		glog.Warningf("Could not connect to %s: %v", mpvSocketName, err)
		time.Sleep(time.Second)
	}
}

func mpvRPC(ctx context.Context, args ...string) (string, error) {
	command := MPVCommand{Command: args}
	commandBytes, err := json.Marshal(command)
	if err != nil {
		return "", err
	}
	commandBytes = append(commandBytes, '\n')

	mpvReconnect(ctx)
	for {
		_, err = mpvSocket.Write(commandBytes)
		if err == nil {
			break
		}
		mpvSocket = nil
		glog.Warningf("Could not send data to %s: %v", mpvSocketName, err)
		mpvReconnect(ctx)
	}
	responseBytes := make([]byte, 1024)
	n, err := mpvSocket.Read(responseBytes)
	if err != nil {
		return "", nil
	}
	responseBytes = responseBytes[:n]
	response := MPVResponse{}
	err = json.Unmarshal(responseBytes, &response)
	if err != nil {
		return "", err
	}
	if response.Error != "success" {
		return "", errors.New(response.Error)
	}
	return response.Data, nil
}

func Play(ctx context.Context, file string, done chan error) error {
	processMutex.Lock()
	defer processMutex.Unlock()
	return play(ctx, file, done)
}

func play(ctx context.Context, file string, done chan error) error {
	stop(ctx)

	args := []string{}
	for _, a := range mpvArgs {
		if a == "#FNAME#" {
			a = file
		} else if a == "#SNAME#" {
			a = mpvSocketName
		}
		args = append(args, a)
	}

	glog.Infof("Starting mpv with arguments: %v", args)
	if tr, ok := trace.FromContext(ctx); ok {
		tr.LazyPrintf("Starting mpv with arguments: %v", args)
	}
	cmd := exec.Command("./mpv", args...)
	err := cmd.Start()
	if err != nil {
		return err
	}
	process = cmd.Process
	glog.Infof("Set process to %v", process)
	go func() {
		res := cmd.Wait()
		if res == nil {
			// Video playback not interrupted - blank the screen.
			blank(ctx)
		}
		done <- res
	}()
	return nil
}

type remoteServer struct {
}

func (r *remoteServer) Play(ctx context.Context, in *pb.PlayRequest) (*pb.PlayResponse, error) {
	if in.Filename == "" {
		return nil, errors.New("No filename specified.")
	}
	res := make(chan error, 1)
	Play(ctx, in.Filename, res)
	err := <-res
	if err != nil {
		return nil, err
	}
	return &pb.PlayResponse{}, nil
}

func (r *remoteServer) SetVolume(ctx context.Context, in *pb.SetVolumeRequest) (*pb.SetVolumeResponse, error) {
	return nil, errors.New("Not implemented.")
}

func (r *remoteServer) Interrupt(ctx context.Context, in *pb.InterruptRequest) (*pb.InterruptResponse, error) {
	Stop(ctx)
	return &pb.InterruptResponse{}, nil
}

func main() {
	flag.IntVar(&bindPort, "port", 8080, "Port on which to bind GRPC server to.")
	flag.Parse()
	glog.Info("Starting webled remote...")
	process = nil
	tmpDir, err := ioutil.TempDir("", "remote")
	if err != nil {
		glog.Error(err)
	}
	mpvSocketName = fmt.Sprintf("%s/mpv.socket", tmpDir)
	defer func() {
		Stop(context.Background())
		os.RemoveAll(tmpDir)
	}()

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", bindPort))
	if err != nil {
		glog.Exit(err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterRemoteVideoServer(grpcServer, &remoteServer{})
	glog.Exit(grpcServer.Serve(lis))
}
