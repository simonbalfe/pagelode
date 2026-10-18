FROM oven/bun:1.3.5-debian

WORKDIR /app
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl xvfb fluxbox pulseaudio \
    libgl1 libglx-mesa0 libgl1-mesa-dri libegl1 libgles2 \
    fonts-liberation fonts-dejavu-core fonts-noto-core fonts-noto-cjk fonts-noto-color-emoji fonts-cantarell \
  && curl -fsSLo /tmp/chrome.deb "https://dl.google.com/linux/direct/google-chrome-stable_current_$(dpkg --print-architecture).deb" \
  && apt-get install -y --no-install-recommends /tmp/chrome.deb \
  && rm -rf /tmp/chrome.deb /var/lib/apt/lists/* \
  && mkdir -p /data /home/bun/.fluxbox \
  && chown -R bun:bun /data /home/bun
COPY package.json bun.lock tsconfig.json ./
RUN bun install --frozen-lockfile --production
COPY --chown=bun:bun desktop/fluxbox-init /home/bun/.fluxbox/init
COPY --chown=bun:bun desktop/fluxbox-apps /home/bun/.fluxbox/apps
COPY desktop ./desktop
COPY src ./src
COPY public ./public
COPY docker-entrypoint.sh ./
ENV BROWSER_SERVICE_HOST=0.0.0.0 \
    BROWSER_SERVICE_PORT=8090 \
    BROWSER_SERVICE_HEADLESS=false \
    BROWSER_SERVICE_CHANNEL=chrome \
    BROWSER_SERVICE_CHROME_ARGS="--use-gl=angle --use-angle=gl --ignore-gpu-blocklist" \
    BROWSER_SERVICE_DATA_DIR=/data/identities \
    BROWSER_SERVICE_PROFILES_DIR=/data/profiles \
    DISPLAY=:99 \
    LANG=en_US.UTF-8
USER bun
VOLUME /data
EXPOSE 8090
ENTRYPOINT ["./docker-entrypoint.sh"]
