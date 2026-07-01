# SPDX-FileCopyrightText: (C) 2026 Intel Corporation
# SPDX-License-Identifier: Apache-2.0

from __future__ import annotations

import re
from typing import Any, Iterable, Mapping, Optional, Sequence

DEFAULT_ENTITY_RERANK_MAX_BOOST = 0.20
DEFAULT_ENTITY_RERANK_DOC_TYPE_BOOST = 0.05
DEFAULT_ENTITY_RERANK_EXACT_LABEL_BOOST = 0.15
DEFAULT_ENTITY_RERANK_SYNONYMS = (
    "automobile=car,vehicle=car,people=person,persons=person"
)

_TOKEN_RE = re.compile(r"[a-z0-9]+")
_ENTITY_DOC_MARKER_RE = re.compile(
    r"\bentity[-\s]+focused\s+video\s+search\s+document\b", re.IGNORECASE
)
_ENTITY_LINE_RE = re.compile(
    r"(?im)^\s*(?:entity|observed\s+label)\s*:\s*([^\n.;,]+)"
)


def normalize_entity_label(value: Any) -> str:
    """Normalize entity labels without allowing substring matches."""
    tokens = [_singularize_token(token) for token in _TOKEN_RE.findall(str(value).lower())]
    return " ".join(token for token in tokens if token)


def parse_synonym_map(synonyms: str | Mapping[str, str] | None) -> dict[str, str]:
    """Parse alias=canonical synonym configuration into normalized labels."""
    if not synonyms:
        return {}

    if isinstance(synonyms, Mapping):
        entries = synonyms.items()
    else:
        parsed_entries: list[tuple[str, str]] = []
        for raw_entry in str(synonyms).split(","):
            if "=" not in raw_entry:
                continue
            alias, canonical = raw_entry.split("=", 1)
            parsed_entries.append((alias, canonical))
        entries = parsed_entries

    synonym_map: dict[str, str] = {}
    for alias, canonical in entries:
        normalized_alias = normalize_entity_label(alias)
        normalized_canonical = normalize_entity_label(canonical)
        if normalized_alias and normalized_canonical:
            synonym_map[normalized_alias] = normalized_canonical

    return synonym_map


def extract_query_entities(
    query: str,
    known_labels: Optional[set[str]] = None,
    synonyms: str | Mapping[str, str] | None = DEFAULT_ENTITY_RERANK_SYNONYMS,
) -> set[str]:
    """Extract normalized query tokens/phrases using token boundaries only."""
    tokens = [_singularize_token(token) for token in _TOKEN_RE.findall((query or "").lower())]
    if not tokens:
        return set()

    synonym_map = parse_synonym_map(synonyms)
    normalized_known = {
        normalize_entity_label(label)
        for label in (known_labels or set())
        if normalize_entity_label(label)
    }
    phrase_sources = set(normalized_known) | set(synonym_map) | set(synonym_map.values())
    max_phrase_len = max((len(label.split()) for label in phrase_sources), default=3)
    max_phrase_len = max(1, min(max_phrase_len, 5))

    candidates: set[str] = set()
    for phrase_len in range(1, max_phrase_len + 1):
        for start in range(0, len(tokens) - phrase_len + 1):
            phrase = " ".join(tokens[start : start + phrase_len])
            if phrase:
                candidates.add(phrase)

    expanded = set(candidates)
    for candidate in candidates:
        canonical = synonym_map.get(candidate)
        if canonical:
            expanded.add(canonical)

    if not normalized_known:
        return expanded

    filtered: set[str] = set()
    canonical_known = {_canonicalize_label(label, synonym_map) for label in normalized_known}
    for candidate in expanded:
        canonical_candidate = _canonicalize_label(candidate, synonym_map)
        if candidate in normalized_known or canonical_candidate in canonical_known:
            filtered.add(candidate)
            filtered.add(canonical_candidate)

    return filtered


def get_document_entity_tags(
    metadata: Mapping[str, Any] | None,
    page_content: str = "",
) -> set[str]:
    """Extract entity labels from metadata tags and page content fallback."""
    metadata = metadata or {}
    labels: set[str] = set()

    for tag in _coerce_tags(metadata.get("tags")):
        normalized_tag = str(tag).strip()
        if normalized_tag.lower().startswith("entity:"):
            label = normalize_entity_label(normalized_tag.split(":", 1)[1])
            if label:
                labels.add(label)

    content = _document_content(metadata, page_content)
    for match in _ENTITY_LINE_RE.finditer(content):
        label = normalize_entity_label(match.group(1))
        if label:
            labels.add(label)

    return labels


def get_document_doc_type(
    metadata: Mapping[str, Any] | None,
    page_content: str = "",
) -> Optional[str]:
    """Extract document type from explicit metadata, tags, or text marker."""
    metadata = metadata or {}

    for key in ("doc_type", "document_type"):
        value = metadata.get(key)
        if value:
            doc_type = _normalize_doc_type(value)
            if doc_type:
                return doc_type

    for tag in _coerce_tags(metadata.get("tags")):
        normalized_tag = str(tag).strip()
        if normalized_tag.lower().startswith("doc:"):
            doc_type = _normalize_doc_type(normalized_tag.split(":", 1)[1])
            if doc_type:
                return doc_type

    if _ENTITY_DOC_MARKER_RE.search(_document_content(metadata, page_content)):
        return "entity-summary"

    return None


def apply_entity_aware_rerank(
    docs_with_score: list[tuple[Any, float]],
    query: str,
    *,
    enabled: bool = True,
    max_boost: float = DEFAULT_ENTITY_RERANK_MAX_BOOST,
    doc_type_boost: float = DEFAULT_ENTITY_RERANK_DOC_TYPE_BOOST,
    exact_label_boost: float = DEFAULT_ENTITY_RERANK_EXACT_LABEL_BOOST,
    synonyms: str | Mapping[str, str] | None = DEFAULT_ENTITY_RERANK_SYNONYMS,
    min_score: Optional[float] = None,
    higher_is_better: bool = True,
) -> list[tuple[Any, float]]:
    """Apply deterministic entity-aware reranking to VDMS results."""
    if not enabled:
        return list(docs_with_score)

    synonym_map = parse_synonym_map(synonyms)
    doc_infos: list[dict[str, Any]] = []
    all_labels: set[str] = set()

    for index, (document, score) in enumerate(docs_with_score):
        metadata = _ensure_metadata(document)
        page_content = _get_page_content(document, metadata)
        labels = get_document_entity_tags(metadata, page_content)
        doc_type = get_document_doc_type(metadata, page_content)
        all_labels.update(labels)
        doc_infos.append(
            {
                "index": index,
                "document": document,
                "score": _coerce_float(score),
                "metadata": metadata,
                "labels": labels,
                "doc_type": doc_type,
            }
        )

    known_labels = all_labels | {_canonicalize_label(label, synonym_map) for label in all_labels}
    query_entities = extract_query_entities(query, known_labels, synonyms)
    canonical_query_entities = {
        _canonicalize_label(label, synonym_map) for label in query_entities
    }

    capped_max_boost = max(0.0, _coerce_float(max_boost))
    base_doc_boost = max(0.0, _coerce_float(doc_type_boost))
    per_label_boost = max(0.0, _coerce_float(exact_label_boost))

    reranked: list[tuple[int, Any, float]] = []
    for info in doc_infos:
        metadata = info["metadata"]
        semantic_score = info["score"]
        labels = info["labels"]
        doc_type = info["doc_type"]

        if doc_type:
            metadata.setdefault("doc_type", doc_type)

        matching_labels = sorted(
            label
            for label in labels
            if _canonicalize_label(label, synonym_map) in canonical_query_entities
        )
        is_entity_summary = doc_type == "entity-summary"
        score_is_eligible = min_score is None or semantic_score >= min_score

        entity_boost = 0.0
        if is_entity_summary and matching_labels and score_is_eligible:
            requested_boost = base_doc_boost + (per_label_boost * len(matching_labels))
            entity_boost = min(capped_max_boost, requested_boost)

        reranked_score = (
            semantic_score + entity_boost
            if higher_is_better
            else semantic_score - entity_boost
        )

        metadata["semantic_score"] = semantic_score
        metadata["entity_boost"] = entity_boost
        metadata["entity_match_labels"] = matching_labels
        metadata["reranked_score"] = reranked_score

        reranked.append((info["index"], info["document"], reranked_score))

    # Current search-ms IP path treats higher scores as better. The lower-score
    # branch preserves a future seam for distance metrics where smaller is better.
    sorted_results = sorted(
        reranked,
        key=lambda item: (item[2], -item[0]) if higher_is_better else (item[2], item[0]),
        reverse=higher_is_better,
    )
    return [(document, score) for _, document, score in sorted_results]


def summarize_rerank_stats(docs_with_score: Sequence[tuple[Any, float]]) -> dict[str, Any]:
    """Summarize metadata added by apply_entity_aware_rerank for logging."""
    boosted_count = 0
    max_boost = 0.0
    labels: set[str] = set()

    for document, _ in docs_with_score:
        metadata = _read_metadata(document)
        boost = _coerce_float(metadata.get("entity_boost", 0.0))
        if boost > 0:
            boosted_count += 1
            max_boost = max(max_boost, boost)
            labels.update(str(label) for label in metadata.get("entity_match_labels", []) or [])

    return {
        "total": len(docs_with_score),
        "boosted": boosted_count,
        "max_boost": max_boost,
        "match_labels": sorted(labels),
    }


def _singularize_token(token: str) -> str:
    if len(token) > 4 and token.endswith("ies"):
        return f"{token[:-3]}y"
    if len(token) > 3 and token.endswith("s") and not token.endswith(("ss", "us")):
        return token[:-1]
    return token


def _canonicalize_label(label: str, synonym_map: Mapping[str, str]) -> str:
    normalized = normalize_entity_label(label)
    return synonym_map.get(normalized, normalized)


def _coerce_tags(tags: Any) -> Iterable[str]:
    if tags is None:
        return []
    if isinstance(tags, str):
        return [tag.strip() for tag in re.split(r"[,|]", tags) if tag.strip()]
    if isinstance(tags, Iterable):
        return [str(tag).strip() for tag in tags if str(tag).strip()]
    return [str(tags).strip()]


def _normalize_doc_type(value: Any) -> str:
    normalized = normalize_entity_label(value).replace(" ", "-")
    return normalized


def _document_content(metadata: Mapping[str, Any], page_content: str = "") -> str:
    if page_content:
        return str(page_content)
    content = metadata.get("page_content", "")
    return str(content or "")


def _get_page_content(document: Any, metadata: Mapping[str, Any]) -> str:
    if isinstance(document, Mapping):
        return _document_content(metadata, str(document.get("page_content", "") or ""))
    return _document_content(metadata, str(getattr(document, "page_content", "") or ""))


def _read_metadata(document: Any) -> Mapping[str, Any]:
    if isinstance(document, Mapping):
        metadata = document.get("metadata", {})
    else:
        metadata = getattr(document, "metadata", {})
    return metadata if isinstance(metadata, Mapping) else {}


def _ensure_metadata(document: Any) -> dict[str, Any]:
    if isinstance(document, dict):
        metadata = document.get("metadata")
        if not isinstance(metadata, dict):
            metadata = {}
            document["metadata"] = metadata
        return metadata

    metadata = getattr(document, "metadata", None)
    if not isinstance(metadata, dict):
        metadata = {}
        try:
            setattr(document, "metadata", metadata)
        except Exception:
            pass
    return metadata


def _coerce_float(value: Any) -> float:
    try:
        return float(value)
    except (TypeError, ValueError):
        return 0.0
