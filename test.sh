#!/bin/sh
set -e
set -u

DP0=`dirname $0`

go install $DP0 || { echo Failed to build/install ; exit 1; }
gosigsrv || { echo gosigsrv failed or something; exit 1; }
