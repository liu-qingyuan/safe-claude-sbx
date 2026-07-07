FROM docker/sandbox-templates:claude-code-docker

USER agent
RUN curl -fsSL https://herdr.dev/install.sh | sh

USER root
RUN ln -sf /home/agent/.local/bin/herdr /usr/local/bin/herdr \
    && printf '%s\n' \
        '#!/usr/bin/env bash' \
        'export CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1' \
        'exec claude --dangerously-skip-permissions "$@"' \
        > /usr/local/bin/cc \
    && chmod 0755 /usr/local/bin/cc \
    && command -v herdr \
    && herdr --version \
    && command -v cc

USER agent
