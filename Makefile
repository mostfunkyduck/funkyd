REVISION=`git show | head -1 | awk '{print $$NF}' | cut -c 1-5`
HOSTNAME=`hostname`
DATE=`date +%Y.%m.%d.%H.%M`
BRANCH=`git branch 2>/dev/null | grep '\*' | sed "s/* //"`

.PHONY: mocks all test docker clean

all: test funkyd

clean:
	go clean

docker:
	sudo docker-compose build
	sudo docker build -t funkyd/dnsperf -f ./Dockerfile.dnsperf .

test: docker
	sudo docker-compose up -d
	go test -v -bench=.*
	./testdata/run_dnsperf.sh

funkyd: *.go
	# putting this here so that it can call the 'revision' alias
	# and get the tag based on that
	$(eval TAG := $(shell git tag --points-at $(REVISION)))
	go get
	go build -ldflags "-X main.versionHostname=$(HOSTNAME) -X main.versionDate=$(DATE) -X main.versionBranch=$(BRANCH) -X main.versionTag=$(TAG) -X main.versionRevision=$(REVISION)"

mocks: *.go
	mockery -inpkg -all -testonly
