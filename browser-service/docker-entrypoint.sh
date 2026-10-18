#!/bin/sh
set -eu
rm -f "/tmp/.X${DISPLAY#:}-lock" "/tmp/.X11-unix/X${DISPLAY#:}"
Xvfb "$DISPLAY" -screen 0 1920x1080x24 -nolisten tcp >/dev/null 2>&1 &
while [ ! -e "/tmp/.X11-unix/X${DISPLAY#:}" ]; do sleep 0.1; done
fluxbox >/dev/null 2>&1 &
pulseaudio --daemonize=yes --exit-idle-time=-1 --disallow-exit -n --file=/app/desktop/pulse.pa >/dev/null 2>&1
exec bun src/server.ts
