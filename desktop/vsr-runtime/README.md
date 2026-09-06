# Yaver local VSR adapter (experimental)

This process accepts only normalized `96x96 gray8` mouth crops over stdin and
runs on the user's Yaver machine. It does not accept audio or full-face video,
does not use a cloud service, and deletes its mouth-only temporary MP4 after
each inference.

Install a compatible Auto-AVSR checkout and its Python dependencies yourself,
then configure the local agent:

```sh
export AUTO_AVSR_ROOT=/path/to/auto_avsr
export AUTO_AVSR_CONFIG=/path/to/LRS3_V_WER19.1.ini
export AUTO_AVSR_MODEL=/path/to/model.pth
export YAVER_VSR_COMMAND="python3 /path/to/yaver.io/desktop/vsr-runtime/yaver_vsr/inference.py"
```

The adapter deliberately does not download or redistribute checkpoints.
Auto-AVSR code and checkpoints have distinct terms: the maintained repository
states its code is Apache-2.0 while pretrained models can inherit dataset terms.
LRS3-derived checkpoints therefore require a separate commercial-use review
before Yaver can bundle them. Chaplin is useful MIT-licensed reference code,
but is not a Yaver runtime dependency.
