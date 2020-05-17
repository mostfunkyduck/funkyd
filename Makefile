REVISION=`git show | head -1 | awk '{print $$NF}' | cut -c 1-5`
HOSTNAME=`hostname`
DATE=`date +%Y.%m.%d.%H.%M`
BRANCH=`git branch 2>/dev/null | grep '\*' | sed "s/* //"`
funkyd: *.go
	# putting this here so that it can call the 'revision' alias
	# and get the tag based on that
	$(eval TAG := $(shell git tag --points-at $(REVISION)))
	go build -ldflags "-X main.versionHostname=$(HOSTNAME) -X main.versionDate=$(DATE) -X main.versionBranch=$(BRANCH) -X main.versionTag=$(TAG) -X main.versionRevision=$(REVISION)"
