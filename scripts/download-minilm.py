"""Explicit development download; never invoked by the app or inference worker."""
import argparse
import hashlib
from pathlib import Path
import urllib.request
import ssl

try:
    import truststore
    TLS_CONTEXT = truststore.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
except ImportError:
    TLS_CONTEXT = ssl.create_default_context()

REVISION = "10dbd2f06a8baf40ad285b037edda610c1b9a57c"
FILES = {
    "onnx/model.onnx": ("model.onnx", "6fd5d72fe4589f189f8ebc006442dbb529bb7ce38f8082112682524616046452", 90405214),
    "tokenizer.json": ("tokenizer.json", "be50c3628f2bf5bb5e3a7f17b1f74611b2561a3a27eeab05e5aa30f411572037", 466247),
}

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("directory", type=Path)
    target = parser.parse_args().directory.resolve()
    target.mkdir(parents=True, exist_ok=True)
    for remote, (name, expected, size) in FILES.items():
        destination = target / name
        if destination.exists() and hashlib.sha256(destination.read_bytes()).hexdigest() == expected:
            print(f"Verified {name}")
            continue
        url = f"https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/{REVISION}/{remote}"
        temporary = target / (name + ".part")
        digest = hashlib.sha256()
        received = 0
        try:
            with urllib.request.urlopen(url, timeout=60, context=TLS_CONTEXT) as response, temporary.open("wb") as output:
                while chunk := response.read(64 * 1024):
                    received += len(chunk)
                    if received > size:
                        raise ValueError("Unexpected artifact size")
                    digest.update(chunk)
                    output.write(chunk)
            if received != size or digest.hexdigest() != expected:
                raise ValueError("Artifact verification failed")
            temporary.replace(destination)
            print(f"Installed and verified {name}")
        finally:
            temporary.unlink(missing_ok=True)

if __name__ == "__main__":
    main()
