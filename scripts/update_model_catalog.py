#!/usr/bin/env python3
"""Refresh portable model metadata from a version-pinned, MIT-licensed package."""

import argparse
import base64
import copy
import hashlib
import io
import json
from pathlib import Path
import tarfile
import urllib.request


def fetch_json(address):
    """Read registry metadata with a bounded network timeout."""
    with urllib.request.urlopen(address, timeout=30) as response:
        return json.load(response)


def read_package(version):
    """Verify the published archive and its actual license before reading data in memory."""
    metadata = fetch_json("https://registry.npmjs.org/@cherrystudio%2Fprovider-registry/" + version)
    with urllib.request.urlopen(metadata["dist"]["tarball"], timeout=30) as response:
        archive = response.read()
    integrity = metadata["dist"]["integrity"]
    algorithm, expected = integrity.split("-", 1)
    actual = base64.b64encode(hashlib.new(algorithm, archive).digest()).decode()
    if actual != expected:
        raise ValueError("Catalog package integrity mismatch")
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as package:
        license_text = package.extractfile("package/LICENSE").read().decode()
        if not license_text.startswith("MIT License"):
            raise ValueError("Catalog package is not explicitly MIT licensed")
        models = json.load(package.extractfile("package/data/models.json"))
        overrides = json.load(package.extractfile("package/data/provider-models.json"))
    return models, overrides


def canonical_key(value):
    """Normalize metadata keys without rewriting upstream API model identifiers."""
    return value.strip().lower().replace(".", "-").replace("_", "-")


def text_model(model):
    """Keep text models and exclude known embedding, reranking, and image-only entries."""
    modalities = model.get("outputModalities", [])
    if modalities:
        return "text" in modalities
    capabilities = model.get("capabilities", [])
    return not any(item in capabilities for item in ["embedding", "rerank", "image-generation"])


def portable_model(model):
    """Retain only token capacities and parameters that the Go adapters can consume."""
    limits = {}
    for upstream, local in [("contextWindow", "context_window_tokens"), ("maxInputTokens", "max_input_tokens"), ("maxOutputTokens", "max_output_tokens")]:
        value = model.get(upstream)
        if isinstance(value, int) and value > 0:
            limits[local] = value
    result = {"id": model["id"], "name": model.get("name", model["id"]), "limits": limits}
    temperature = model.get("parameterSupport", {}).get("temperature", {}).get("supported")
    if isinstance(temperature, bool):
        result["temperature_supported"] = temperature
    efforts = []
    for control in model.get("reasoning", {}).get("controls", []):
        if control.get("kind") == "effort":
            efforts.extend(control.get("values", []))
    if efforts:
        result["reasoning_efforts"] = list(dict.fromkeys(efforts))
    return result


def build_catalog(version, models, overrides):
    """Build metadata plus explicit provider aliases, excluding provider routing and credentials."""
    entries = {canonical_key(model["id"]): portable_model(model) for model in models["models"] if text_model(model)}
    aliases = {}
    providers = {"openai": "openai", "anthropic": "anthropic", "google": "google", "deepseek": "deepseek", "dashscope": "bailian", "doubao": "ark", "zhipu": "zhipu", "moonshot": "moonshot", "minimax": "minimax", "grok": "xai", "ollama": "ollama", "openrouter": "openrouter"}
    for override in overrides["overrides"]:
        provider = providers.get(override["providerId"])
        canonical = canonical_key(override.get("modelId", ""))
        if not provider or canonical not in entries or not override.get("apiModelId") or not text_model(override):
            continue
        alias = provider + "/" + override["apiModelId"]
        metadata = copy.deepcopy(entries[canonical])
        for upstream, local in [("contextWindow", "context_window_tokens"), ("maxInputTokens", "max_input_tokens"), ("maxOutputTokens", "max_output_tokens")]:
            if override.get(upstream):
                metadata["limits"][local] = override[upstream]
        entries[alias] = metadata
        aliases[alias] = alias
    return {"source": "@cherrystudio/provider-registry", "version": version, "license": "MIT", "copyright": "Copyright (c) Cherry Studio contributors", "upstream_versions": {"models": models["version"], "overrides": overrides["version"]}, "models": entries, "aliases": aliases}


def main():
    """Regenerate the bundled data reproducibly without installing a JavaScript runtime dependency."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", default="0.3.5", help="Exact upstream package version")
    args = parser.parse_args()
    models, overrides = read_package(args.version)
    catalog = build_catalog(args.version, models, overrides)
    target = Path(__file__).resolve().parents[1] / "internal/core/model_catalog.json"
    target.write_text(json.dumps(catalog, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")
    print(f"Updated {len(catalog['models'])} metadata entries and {len(catalog['aliases'])} aliases from {args.version}")


if __name__ == "__main__":
    main()
