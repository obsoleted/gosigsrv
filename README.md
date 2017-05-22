# gosigsrv

[![Build Status](https://travis-ci.org/obsoleted/gosigsrv.svg?branch=master)](https://travis-ci.org/obsoleted/gosigsrv)

## WebRTC Signaling Server written in Go

Intended to mostly be a stand in for the [peerconnection_server](https://github.com/pristineio/webrtc-mirror/tree/master/webrtc/examples/peerconnection/server) webrtc sample with a couple modifications:

- Some logic to split out peers into two types **clients** and **servers** (servers are just peers that have names beginning with `renderingserver_`)
- Peers only see information about peers of the opposing type
- When a peer sends a message to another peer they will cease being advertised to new peers

#### **WARNING**

This is also more or less my attempt to learn Go. So yeah...

- This is not likely to be idiomatic
- This is not likely to be best practice
- Many other caveats


## Instalation and running
```sh
go get github.com/obsoleted/gosigsrv
gosigsrv
```

Also available as a docker container [obsoleted/gosigsrv](https://hub.docker.com/r/obsoleted/gosigsrv/) (obsoleted/gosigsrv:latest tracks master)