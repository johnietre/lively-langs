.PHONY: bin/lively-langs
bin/lively-langs:
	go build -o $@ github.com/johnietre/lively-langs/cmd/lively-langs

lively-langs: bin/lively-langs
