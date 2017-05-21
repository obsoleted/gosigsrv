#!/bin/sh
set -e
set -u

DP0=`dirname $0`

echo building
go install $DP0 || { echo Failed to build/install ; exit 1; }
echo
echo build complete
echo
echo testing
echo
go test -v $DP0 || { echo Tests failed ; exit 1; }
echo
echo test complete
echo
echo running
echo
gosigsrv || { echo gosigsrv failed or something; exit 1; }
