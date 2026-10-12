"""Fixed private-pipe CPU encoder. No downloads, HTTP endpoints or mail storage."""
import json
import os
import sys

# Set before importing native runtimes; also explicitly disable ORT telemetry.
os.environ["ORT_DISABLE_TELEMETRY"] = "1"
os.environ["HF_HUB_OFFLINE"] = "1"
os.environ["HF_HUB_DISABLE_TELEMETRY"] = "1"
os.environ["TOKENIZERS_PARALLELISM"] = "false"


def main():
    import numpy as np
    import onnxruntime as ort
    import tokenizers
    from tokenizers import Tokenizer

    if ort.__version__ != "1.23.2" or np.__version__ != "2.5.4" or tokenizers.__version__ != "0.23.3":
        raise ValueError("runtime versions")

    ort.disable_telemetry_events()
    ort.set_default_logger_severity(4)
    model_dir = sys.argv[1]
    tokenizer = Tokenizer.from_file(os.path.join(model_dir, "tokenizer.json"))
    tokenizer.enable_truncation(max_length=256)
    tokenizer.enable_padding(pad_id=0, pad_token="[PAD]")
    options = ort.SessionOptions()
    options.intra_op_num_threads = 2
    options.inter_op_num_threads = 1
    options.log_severity_level = 4
    session = ort.InferenceSession(
        os.path.join(model_dir, "model.onnx"), options,
        providers=["CPUExecutionProvider"],
    )
    request = json.loads(sys.stdin.buffer.read(1024 * 1024 + 1))
    texts = request["texts"]
    if not isinstance(texts, list) or not 1 <= len(texts) <= 32:
        raise ValueError("input")
    if any(not isinstance(text, str) or len(text.encode("utf-8")) > 16384 for text in texts):
        raise ValueError("input")
    encoded = tokenizer.encode_batch(texts)
    inputs = {
        "input_ids": np.array([item.ids for item in encoded], dtype=np.int64),
        "attention_mask": np.array([item.attention_mask for item in encoded], dtype=np.int64),
        "token_type_ids": np.array([item.type_ids for item in encoded], dtype=np.int64),
    }
    outputs = session.run(None, {item.name: inputs[item.name] for item in session.get_inputs()})
    tokens = outputs[0]
    mask = inputs["attention_mask"][..., None].astype(np.float32)
    # Sentence-transformers' attention-masked mean pooling, then L2 normalization.
    vectors = (tokens * mask).sum(axis=1) / np.maximum(mask.sum(axis=1), 1e-9)
    vectors = vectors / np.maximum(np.linalg.norm(vectors, axis=1, keepdims=True), 1e-12)
    if vectors.shape != (len(texts), 384) or not np.isfinite(vectors).all():
        raise ValueError("output")
    print(json.dumps({"vectors": vectors.tolist()}, allow_nan=False, separators=(",", ":")))


if __name__ == "__main__":
    try:
        main()
    except Exception:
        # Never print exceptions, inputs, filesystem paths or native diagnostics.
        sys.exit(1)
