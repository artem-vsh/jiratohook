mkfile_path := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
GOPATH := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
export GOPATH

make:
	go install ru/wikimart/dataflow/jiratohook
