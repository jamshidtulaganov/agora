# Agora fine-tune kit — `agora-pm-v1`

Turnkey path from captured run data to your own PM model. The data flywheel
(capture → label → export) feeds this; when enough good examples accumulate,
this is 4 commands to a fine-tuned model that drops into the **same "Agora"
overlay slot** users already see.

```
/api/datasets/runs ──prepare.py──► train.jsonl ──train_lora.py──► GGUF
       (Slice 3)                                                    │
                                                          ollama create
                                                                    │
                                          opencode ◄── ollama/agora-pm-v1
                                                                    │
                                            agoraFreeModels map (1-line swap)
```

## When to run

- **Data:** ~500–1000+ `accepted` examples. Check live:
  `SELECT outcome_label, count(*) FROM agent_run_trace GROUP BY 1;`
  Below a few hundred, skip the fine-tune — keep improving the **system prompt +
  builtin skills** instead (free, no training, often 80% of the gain).
- **Compute:** one GPU session. Free Colab T4 trains a 7-8B 4-bit LoRA in ~1–2h
  (~$0). A rented A10/L4 is a few dollars. No laptop GPU needed.
- **Base:** an **open-weight** model you can own — `Qwen3` (Apache-2) or
  `DeepSeek` (MIT). NOT GLM-4-Flash (you rent it from z.ai, can't fine-tune it).

## Steps

```bash
# 1. Pull the accepted runs and shape them into chat JSONL
AGORA_URL=https://sd-agora-backend.fly.dev \
AGORA_TOKEN=<personal access token> \
AGORA_WORKSPACE_ID=356e2301-64fd-440b-ab68-7fcfa76088b1 \
python finetune/prepare.py --outcome accepted --out train.jsonl

# 2. LoRA fine-tune on a GPU (Colab/rented). Validate versions vs unsloth docs.
pip install unsloth trl datasets
python finetune/train_lora.py --data train.jsonl --base unsloth/Qwen3-8B-bnb-4bit

# 3. Serve the merged GGUF via Ollama
ollama create agora-pm-v1 -f finetune/Modelfile

# 4a. Make opencode see it (Ollama provider auto-discovers local models):
opencode models | grep agora-pm

# 4b. Swap the overlay — ONE line in server/pkg/agent/models.go:
#       var agoraFreeModels = map[string]agoraBrand{
#     +     "ollama/agora-pm-v1": {Label: "Agora"},
#       }
#     Rebuild/redeploy. Users keep seeing "Agora [Free]" — now backed by YOUR model.

# 4c. Point the live agent at it:
agora agent update 1c8b0043-5560-4218-87b6-2be6c129a18f --model ollama/agora-pm-v1
```

## The loop

`agora-pm-v1` runs in the product → captures more runs → labels more outcomes →
`prepare.py` again → `agora-pm-v2`. Each turn the model is more yours and the
data is the moat. The free GLM base was only the cold start.

## Tuning notes

- **Assistant target shaping** (`prepare.py:to_chat`) is the biggest quality
  lever — decide whether the model should learn the full reasoning trace or just
  the final comment/decision.
- Start with `--outcome accepted` (SFT on what worked). Later, use the
  `rejected`/`corrected` labels for preference tuning (DPO) once you have volume.
- Keep epochs low (1–3) on small datasets to avoid overfitting.
