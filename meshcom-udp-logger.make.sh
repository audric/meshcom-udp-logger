#!/bin/sh
echo ---------------------
echo Creating project home
echo ---------------------
mkdir -p /opt/meshcom-udp-logger
cd /opt/meshcom-udp-logger
echo
echo ---------------
echo Go init project
echo ---------------
go mod init meshcom-udp-logger
echo
echo ----------------
echo Go prerequisites
echo ----------------
go get github.com/eclipse/paho.mqtt.golang modernc.org/sqlite
echo
echo ----------------
echo Compiling binary
echo ----------------
go build -o meshcom-udp-logger meshcom-udp-logger.go
echo
echo -------------------------
echo Stripping compiled binary
echo -------------------------
strip meshcom-udp-logger
echo
echo ------------
echo Test command
echo ------------
echo ./meshcom-udp-logger -bind :1799 -db /opt/meshcom-udp-logger/meshcom-udp-logger.sqlite -mqtt tcp://127.0.0.1:1883
echo
echo ---------------------------------------------
echo Install meshcom-udp-logger.service in systemd
echo ---------------------------------------------
echo systemctl daemon-reload
echo systemctl enable --now meshcom-udp-logger
echo
echo ------------------
echo Check logs command
echo ------------------
echo journalctl -u meshcom-udp-logger -f
echo
