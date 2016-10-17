default: bin/remote bin/webled

clean:
	rm -f proto/remote.pb.go
	rm -f bin/remote bin/webled

proto/remote.pb.go: proto/remote.proto
	protoc -I proto proto/remote.proto --go_out=plugins=grpc:proto

bin/remote: proto/remote.pb.go remote/main.go
	go build -o $@ github.com/q3k/webled/remote

bin/remote.arm: proto/remote.pb.go remote/main.go
	GOARCH=arm go build -o $@ github.com/q3k/webled/remote

bin/webled: play/play.go proto/remote.pb.go work/work.go api.go librarian.go main.go
	go build -o $@ github.com/q3k/webled
