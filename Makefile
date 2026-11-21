.PHONY: test vet harness experiments db build run dev export

# Local Postgres from docker-compose.yml. Override DATABASE_URL to use another database.
DATABASE_URL ?= postgres://hackathon:hackathon@localhost:5432/hackathon?sslmode=disable
export DATABASE_URL

db:
	docker compose up -d --wait postgres

web/node_modules: web/package.json web/package-lock.json
	cd web && npm ci --no-audit --no-fund
	touch web/node_modules

# Static web export embedded into the Go binary (same steps as the Dockerfile).
build: web/node_modules
	cd web && npm run build
	find server/cmd/server/web -mindepth 1 ! -name .gitkeep -delete
	cp -R web/out/. server/cmd/server/web/
	go build -o bin/server ./server/cmd/server

# Postgres in docker + the built binary on http://localhost:8080. Game env vars pass through:
#   T_INTRO=15 T_ASK=15 T_ANSWER=20 T_REVEAL=10 T_VOTE=15 DEV_BOT_HUMAN_AFTER_SEC=5 make run
run: db build
	./bin/server

# Hot reload: Go on :8080, next dev on :3000 (open http://localhost:3000). The browser opens the
# WebSocket straight to :8080, so that origin is allowed here.
dev: db web/node_modules
	trap 'kill 0' EXIT; WS_ORIGINS=localhost:3000 go run ./server/cmd/server & (cd web && npm run dev)

# JSONL per finished game without bot_human seats.
export:
	@go run ./server/cmd/export

test:
	go test ./...

vet:
	go vet ./...

# Red-team the AI seat prompt (needs OPENROUTER_API_KEY, read from env or .env)
#   make harness ARGS="-persona 1 -runs 3 -only B19"
harness:
	go run ./server/cmd/harness $(ARGS)

# Full before/after matrix (≈ $10, ≈ 1.5 h at 20 req/min per judge model; outputs outside the repo).
# See docs/ai-tactics-results.md.
EXP_OUT ?= /tmp/fth-experiments
JUDGE   ?= openai/gpt-5.6-terra,google/gemini-3.1-pro-preview,anthropic/claude-sonnet-5.5
H = go run ./server/cmd/harness -cases all -persona -1 -runs 5 -judge-model $(JUDGE) -lineup -q -max-cost 4 -parallel 12
experiments:
	go run ./server/cmd/harness -mode humans -judge-model $(JUDGE) -out $(EXP_OUT)/humans
	$(H) -prompt-version v1 -label v1-critic -out $(EXP_OUT)/v1-critic
	$(H) -prompt-version v1 -no-critic -label v1-nocritic -out $(EXP_OUT)/v1-nocritic
	$(H) -prompt-version v2 -label v2-critic -out $(EXP_OUT)/v2-critic
	$(H) -prompt-version v2 -no-critic -label v2-nocritic -out $(EXP_OUT)/v2-nocritic
	$(H) -prompt-version v2 -no-critic -model qwen/qwen3.6-35b-a3b -label v2-qwen -out $(EXP_OUT)/v2-qwen
	$(H) -prompt-version v2 -no-critic -model mistralai/mistral-small-2603 -label v2-mistral -out $(EXP_OUT)/v2-mistral
	$(H) -prompt-version v2 -no-critic -persona-set venue -label v2-venue -out $(EXP_OUT)/v2-venue
	go run ./server/cmd/harness -mode summarize -in $(EXP_OUT)/humans,$(EXP_OUT)/v1-critic,$(EXP_OUT)/v1-nocritic,$(EXP_OUT)/v2-critic,$(EXP_OUT)/v2-nocritic,$(EXP_OUT)/v2-qwen,$(EXP_OUT)/v2-mistral,$(EXP_OUT)/v2-venue
