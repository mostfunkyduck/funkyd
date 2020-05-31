REVISION=`git show | head -1 | awk '{print $$NF}' | cut -c 1-5`
HOSTNAME=`hostname`
DATE=`date +%Y.%m.%d.%H.%M`
BRANCH=`git branch 2>/dev/null | grep '\*' | sed "s/* //"`

.PHONY: mocks all unittest performancetest test docker clean cscope

all: githooks test funkyd

githooks: .githooks/*
	# removing old git hooks
	rm .git/hooks/pre-commit || /bin/true
	# deploying git hooks
	ln -s $$PWD/.githooks/pre-commit ./.git/hooks/pre-commit

clean:
	go clean
	rm cscope.files cscope.out

cscope:
	# this will add a local index called 'cscope.out' based on a collection of source files in 'cscope.files'
	# adding local source code
	find . -name "*.go" -print > cscope.files
	# running cscope, the -b and -k flags will keep things narrowly scoped
	cscope -b -k

ctags:
	ctags -R

docker:
	sudo docker-compose build

dnsperf:
	sudo docker build -t funkyd/dnsperf -f ./Dockerfile.dnsperf .

unittest: mocks
	# running unit tests with 1s timeout
	go test -v -timeout 1s

benchmarks: mocks
	# running benchmarks with 30s timeout
	go test -v -timeout 30s -bench=.* -run=^$

performancetest:
	sudo docker-compose up -d
	./testdata/run_dnsperf.sh
	sudo docker-compose down

test: unittest benchmarks docker performancetest

funkyd: *.go
	$(eval TAG := $(shell git tag --points-at $(REVISION)))
	# updating packages
	go get
	# building funkyd
	go build -ldflags "-X main.versionHostname=$(HOSTNAME) -X main.versionDate=$(DATE) -X main.versionBranch=$(BRANCH) -X main.versionTag=$(TAG) -X main.versionRevision=$(REVISION)"

mocks: *.go
	rm mock_*_test.go
	mockery -inpkg -all -testonly
