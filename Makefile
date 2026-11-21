.PHONY: test vet harness experiments

test:
	go test ./...

vet:
	go vet ./...

# Red-team the AI seat prompt (needs OPENROUTER_API_KEY, read from env or .env)
#   make harness ARGS="-persona 1 -runs 3 -only B19"
harness:
	go run ./server/cmd/harness $(ARGS)

# Full before/after matrix (≈ $10–15, ≈ 1–2 h; outputs outside the repo). See docs/ai-tactics-results.md.
EXP_OUT ?= /tmp/fth-experiments
JUDGE   ?= openai/gpt-5.6-terra
H = go run ./server/cmd/harness -cases all -persona -1 -runs 5 -judge-model $(JUDGE) -lineup -q -max-cost 4
experiments:
	go run ./server/cmd/harness -mode humans -judge-model $(JUDGE) -out $(EXP_OUT)/humans
	$(H) -prompt-version v1 -out $(EXP_OUT)/v1-critic
	$(H) -prompt-version v1 -no-critic -out $(EXP_OUT)/v1-nocritic
	$(H) -prompt-version v2 -out $(EXP_OUT)/v2-critic
	$(H) -prompt-version v2 -no-critic -out $(EXP_OUT)/v2-nocritic
	$(H) -prompt-version v2 -no-critic -model qwen/qwen3.6-35b-a3b -out $(EXP_OUT)/v2-qwen
	$(H) -prompt-version v2 -no-critic -model mistralai/mistral-small-2603 -out $(EXP_OUT)/v2-mistral
	$(H) -prompt-version v2 -no-critic -persona-set venue -out $(EXP_OUT)/v2-venue
	go run ./server/cmd/harness -mode summarize -in $(EXP_OUT)/humans,$(EXP_OUT)/v1-critic,$(EXP_OUT)/v1-nocritic,$(EXP_OUT)/v2-critic,$(EXP_OUT)/v2-nocritic,$(EXP_OUT)/v2-qwen,$(EXP_OUT)/v2-mistral,$(EXP_OUT)/v2-venue
