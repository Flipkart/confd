#!/bin/bash

# feed zookeeper
export ZK_PATH="`dirname \"$0\"`"
sh -c "cd $ZK_PATH ; go run main.go"

# Run confd with --watch, expecting it to fail
bin/confd --onetime --log-level debug --confdir ./integration/confdir --interval 5 --backend zookeeper --node 127.0.0.1:2181 -watch
if [ $? -eq 0 ]
then
        exit 1
fi
bin/confd --onetime --log-level debug --confdir ./integration/confdir --interval 5 --backend zookeeper --node 127.0.0.1:2181
