#!/usr/bin/env python3
"""LoRA fine-tune an open base on the prepared Agora chat data -> agora-pm-v1.

TEMPLATE — runs on a GPU, not your laptop. A free Colab T4 trains a 7-8B in
4-bit. Validate package versions against the current Unsloth docs
(https://docs.unsloth.ai) — the API is stable but base-model ids and a couple of
kwargs move over time.

  pip install unsloth trl datasets
  python finetune/train_lora.py --data train.jsonl --base unsloth/Qwen3-8B-bnb-4bit

Why Qwen3 / DeepSeek as the base (NOT GLM-4-Flash): GLM-Flash is served by z.ai
and you cannot fine-tune weights you only rent. agora-pm is YOUR model, so the
fine-tune target must be open weights you can own (Apache-2 Qwen, MIT DeepSeek).
The captured data is provider-agnostic — it trains any base.
"""
import argparse

from unsloth import FastLanguageModel
from unsloth.chat_templates import get_chat_template
from datasets import load_dataset
from trl import SFTTrainer, SFTConfig

ap = argparse.ArgumentParser()
ap.add_argument("--data", default="train.jsonl")
ap.add_argument("--base", default="unsloth/Qwen3-8B-bnb-4bit")
ap.add_argument("--out", default="agora-pm-v1")
ap.add_argument("--epochs", type=float, default=2.0)
ap.add_argument("--max-seq", type=int, default=8192)
args = ap.parse_args()

model, tok = FastLanguageModel.from_pretrained(
    args.base, max_seq_length=args.max_seq, load_in_4bit=True,
)
model = FastLanguageModel.get_peft_model(
    model, r=16, lora_alpha=16, lora_dropout=0.0, bias="none",
    target_modules=["q_proj", "k_proj", "v_proj", "o_proj",
                    "gate_proj", "up_proj", "down_proj"],
)
tok = get_chat_template(tok, chat_template="qwen-2.5")

ds = load_dataset("json", data_files=args.data, split="train")
ds = ds.map(
    lambda b: {"text": [tok.apply_chat_template(m, tokenize=False,
                                                 add_generation_prompt=False)
                        for m in b["messages"]]},
    batched=True,
)

trainer = SFTTrainer(
    model=model, tokenizer=tok, train_dataset=ds,
    args=SFTConfig(
        per_device_train_batch_size=2, gradient_accumulation_steps=4,
        num_train_epochs=args.epochs, learning_rate=2e-4, warmup_ratio=0.05,
        logging_steps=10, optim="adamw_8bit", output_dir="outputs",
        dataset_text_field="text", max_seq_length=args.max_seq,
    ),
)
trainer.train()

# Merge LoRA + export a q4_k_m GGUF for Ollama serving.
model.save_pretrained_gguf(args.out, tok, quantization_method="q4_k_m")
print(f"wrote {args.out}/ (GGUF). Next: ollama create agora-pm-v1 -f finetune/Modelfile")
