# MiniLM tokenizer assets

Source: sentence-transformers/all-MiniLM-L6-v2, Apache-2.0.
Pinned revision: `1110a243fdf4706b3f48f1d95db1a4f5529b4d41`.
Origin: https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/tree/1110a243fdf4706b3f48f1d95db1a4f5529b4d41

Bundled verbatim text assets and SHA-256:

- vocab.txt: `07eced375cec144d27c900241f3e339478dec958f92fddbc551f295c992038a3`
- tokenizer_config.json: `acb92769e8195aabd29b7b2137a9e6d6e25c476a4f15aa4355c233426c61576b`
- special_tokens_map.json: `303df45a03609e4ead04bc3dc1536d0ab19b5358db685b6f3da123d05ec200e3`

Reference tokenizer.json (not a runtime dependency):
`be50c3628f2bf5bb5e3a7f17b1f74611b2561a3a27eeab05e5aa30f411572037`.

The Go implementation uses that file's BERT normalizer, pretokenizer, WordPiece
vocabulary and CLS/SEP postprocessor. Reference fixtures were generated using
Hugging Face tokenizers 0.21.4 against the pinned file with padding and truncation
disabled. Token counts include no padding; chunk inputs reserve CLS/SEP explicitly. No model weights are bundled
or loaded in this slice. Future inference must use this tokenizer/recipe or change
the model/preprocessing version and re-chunk. See LICENSE.txt for Apache-2.0 terms.
