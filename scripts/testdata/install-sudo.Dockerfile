ARG WEBUDDY_TEST_BASE=webuddy-platform:local
FROM ${WEBUDDY_TEST_BASE}
RUN apt-get update && apt-get install -y --no-install-recommends sudo \
 && rm -rf /var/lib/apt/lists/* \
 && printf 'node ALL=(root) NOPASSWD: /usr/bin/true, /usr/local/bin/docker\n' > /etc/sudoers.d/installer \
 && chmod 440 /etc/sudoers.d/installer
ENTRYPOINT []
USER node
