funkyd:
	go build -ldflags "-X main.version=`date +%Y.%m.%d.%H.%M`.`git branch 2> /dev/null | grep --color=auto '*' | sed "s/* //"`"
