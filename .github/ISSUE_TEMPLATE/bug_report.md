name: Bug report
description: Report a problem with kb
title: "[bug] "
labels: [bug]
body:
  - type: markdown
    attributes:
      value: |
        Thanks for taking the time to file a bug report. Please fill in as much
        detail as you can so we can reproduce and fix it.
  - type: textarea
    id: what-happened
    attributes:
      label: What happened?
      description: A clear and concise description of the bug.
    validations:
      required: true
  - type: textarea
    id: reproduce
    attributes:
      label: Steps to reproduce
      description: How can we reproduce the behavior? Include commands, config, and data if possible.
      placeholder: |
        1. Run `./bin/kb ...`
        2. ...
    validations:
      required: true
  - type: textarea
    id: expected
    attributes:
      label: Expected behavior
    validations:
      required: true
  - type: textarea
    id: environment
    attributes:
      label: Environment
      description: OS, Go version, LLM endpoint/model, kb version/commit.
      placeholder: |
        - OS: macOS 14 / Linux
        - Go: 1.26
        - LLM endpoint: http://host:11434, model qwen3.8:latest
        - kb: commit abc1234
  - type: textarea
    id: logs
    attributes:
      label: Logs / output
      description: Relevant output (please redact any secrets or private corpus content).
