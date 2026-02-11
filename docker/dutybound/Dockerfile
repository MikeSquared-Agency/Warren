# Stage 1: Build MC (Go binary)
FROM golang:1.22-bookworm AS go-builder
WORKDIR /src
COPY MissionControl/orchestrator/go.mod MissionControl/orchestrator/go.sum ./orchestrator/
COPY MissionControl/cmd/mc/go.mod MissionControl/cmd/mc/go.sum ./cmd/mc/
RUN cd orchestrator && go mod download
RUN cd cmd/mc && go mod download
COPY MissionControl/ .
RUN mkdir -p cmd/mc/dist && echo '<!doctype html><html><body>dutybound</body></html>' > cmd/mc/dist/index.html
RUN cd cmd/mc && CGO_ENABLED=0 go build -ldflags "-s -w" -o /mc .

# Stage 2: Build mc-core (Rust binary)
FROM rust:1.85-bookworm AS rust-builder
WORKDIR /src
COPY MissionControl/core/ ./core/
RUN cd core && cargo build --release
RUN cp core/target/release/mc-core /mc-core 2>/dev/null || true

# Stage 3: Build OpenClaw gateway
FROM node:22-bookworm AS openclaw-builder
RUN curl -fsSL https://bun.sh/install | bash
ENV PATH="/root/.bun/bin:${PATH}"
RUN corepack enable
WORKDIR /app
COPY openclaw-src/package.json openclaw-src/pnpm-lock.yaml openclaw-src/pnpm-workspace.yaml openclaw-src/.npmrc ./
COPY openclaw-src/ui/package.json ./ui/package.json
COPY openclaw-src/patches ./patches
COPY openclaw-src/scripts ./scripts
RUN pnpm install --frozen-lockfile
COPY openclaw-src/ .
RUN pnpm build
ENV OPENCLAW_PREFER_PNPM=1
RUN pnpm ui:build

# Stage 4: Runtime
FROM node:22-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    supervisor ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

# Create dutybound user
RUN useradd -m -s /bin/bash dutybound

# Copy built OpenClaw gateway
WORKDIR /openclaw
COPY --from=openclaw-builder /app/ ./
RUN chown -R dutybound:dutybound /openclaw

# Copy MC binaries from builder stages
COPY --from=go-builder /mc /usr/local/bin/mc
COPY --from=rust-builder /mc-core /usr/local/bin/mc-core

# Set up Kai's openclaw config (baked into image)
USER dutybound
RUN mkdir -p /home/dutybound/.openclaw/agents/main/agent \
             /home/dutybound/.openclaw/agents/main/sessions \
             /home/dutybound/.openclaw/agents/developer/agent \
             /home/dutybound/.openclaw/agents/developer/sessions \
             /home/dutybound/.openclaw/agents/tester/agent \
             /home/dutybound/.openclaw/agents/tester/sessions \
             /home/dutybound/.openclaw/agents/researcher/agent \
             /home/dutybound/.openclaw/agents/researcher/sessions \
             /home/dutybound/.openclaw/agents/reviewer/agent \
             /home/dutybound/.openclaw/agents/reviewer/sessions \
             /home/dutybound/.openclaw/agents/security/agent \
             /home/dutybound/.openclaw/agents/security/sessions \
             /home/dutybound/.openclaw/workspace

COPY --chown=dutybound:dutybound openclaw.json /home/dutybound/.openclaw/openclaw.json

# Initialize MC mission directory
RUN mkdir -p /home/dutybound/mission
WORKDIR /home/dutybound/mission
RUN /usr/local/bin/mc init 2>/dev/null || mkdir -p .mission/state .mission/specs .mission/findings .mission/handoffs .mission/audit .mission/checkpoints .mission/prompts

# Back to root for supervisord
USER root

# Copy supervisord config and entrypoint
COPY supervisord.conf /etc/supervisor/conf.d/supervisord.conf
COPY entrypoint.sh /entrypoint.sh

EXPOSE 8081

ENTRYPOINT ["/entrypoint.sh"]
