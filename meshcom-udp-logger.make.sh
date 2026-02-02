#!/bin/sh

echo Creating project
echo ----------------
printf "Use current directory instead of default /opt/meshcom-udp-logger? (Y/n): "
read ans
if [ "$ans" = "n" ] || [ "$ans" = "N" ]; then
    mkdir -p /opt/meshcom-udp-logger
    cd /opt/meshcom-udp-logger || { echo "Failed to enter /opt/meshcom-udp-logger"; exit 1; }
else
    echo "Using current directory"
fi

echo Go init/tidy project
echo --------------------
if [ ! -f go.mod ]; then
    go mod init meshcom-udp-logger
else
    go mod tidy
fi

echo Go prerequisites
echo ----------------
go get github.com/eclipse/paho.mqtt.golang modernc.org/sqlite

echo Compile and strip binary
echo ------------------------
go build -o meshcom-udp-logger meshcom-udp-logger.go
strip meshcom-udp-logger

echo Test command
echo ------------
echo  ./meshcom-udp-logger -bind :1799 -db /opt/meshcom-udp-logger/meshcom-udp-logger.sqlite -mqtt tcp://127.0.0.1:1883

echo Install meshcom-udp-logger.service in systemd
echo ---------------------------------------------
echo  sudo cp meshcom-udp-logger.service /etc/systemd/system/
echo  sudo systemctl daemon-reload
echo  sudo systemctl enable --now meshcom-udp-logger

echo Check service status command
echo ----------------------------
echo  sudo systemctl status meshcom-udp-logger

echo Check logs command
echo ------------------
echo  journalctl -u meshcom-udp-logger -f

