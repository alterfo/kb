#!/bin/sh
set -e
cd "$(dirname "$0")"
DRAGON_COMMIT=8e896d29047c8cb934cadc731f734f05abe3140c
uv venv .venv
[ -d vendor/DRAGON ] || git clone https://github.com/RussianNLP/DRAGON vendor/DRAGON
git -C vendor/DRAGON checkout "$DRAGON_COMMIT"
uv pip install --python .venv/bin/python -r requirements.txt -e vendor/DRAGON/lib
