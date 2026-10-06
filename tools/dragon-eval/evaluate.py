#!/usr/bin/env python3
import argparse
import json
from pathlib import Path

from rag_bench import data, evaluator

HIST_PRIVATE_QA_REPO_ID = "ai-forever/hist-rag-bench-private-qa"
HIST_PRIVATE_TEXTS_REPO_ID = "ai-forever/hist-rag-bench-private-texts"


def load_submission(path):
    with open(path, encoding="utf-8") as f:
        raw = json.load(f)
    return {
        public_id: {
            "found_ids": [int(x) for x in entry["found_ids"]],
            "model_answer": entry["model_answer"],
        }
        for public_id, entry in raw.items()
    }


def build_text_mapping(version):
    from datasets import load_dataset

    private_texts = load_dataset(HIST_PRIVATE_TEXTS_REPO_ID, revision=version)
    return {item["public_id"]: item["id"] for item in private_texts["train"]}


def load_qa_dataset(version):
    from datasets import load_dataset

    return load_dataset(HIST_PRIVATE_QA_REPO_ID, revision=version)


def build_report(results, qa_dataset, text_mapping, version, n_questions):
    evaluation = evaluator.evaluate_rag_results(results, qa_dataset, text_mapping)
    report = {
        "dragon_version": version,
        "n_questions": n_questions,
        "average_metrics": evaluation.average_metrics,
    }
    return report, evaluation


def main():
    parser = argparse.ArgumentParser(
        description="Run the official DRAGON hist evaluator over an existing kb self-run submission"
    )
    parser.add_argument("--submission", default="docs/bench/dragon-hist-answers.json")
    parser.add_argument("--out", default="docs/bench/dragon-leaderboard/official-run-report.json")
    args = parser.parse_args()

    results = load_submission(args.submission)

    _, _, version = data.get_datasets(is_hist=True)
    qa_dataset = load_qa_dataset(version)
    text_mapping = build_text_mapping(version)

    report, evaluation = build_report(results, qa_dataset, text_mapping, version, len(results))

    out_path = Path(args.out)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    with open(out_path, "w", encoding="utf-8") as f:
        json.dump(report, f, ensure_ascii=False, indent=2)

    print(evaluation.to_table(overall_only=False))
    print(f"\nSaved report to {out_path}")


if __name__ == "__main__":
    main()
