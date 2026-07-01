# SPDX-FileCopyrightText: (C) 2026 Intel Corporation
# SPDX-License-Identifier: Apache-2.0

import unittest

from src.vdms_retriever.entity_reranker import (
    apply_entity_aware_rerank,
    extract_query_entities,
    get_document_entity_tags,
)


class DummyDocument:
    def __init__(self, page_content="", metadata=None):
        self.page_content = page_content
        self.metadata = metadata or {}


class EntityRerankerTest(unittest.TestCase):
    def test_extracts_tokens_phrases_and_synonyms(self):
        entities = extract_query_entities(
            "Find cars near the traffic-lights and people.",
            known_labels={"car", "traffic light", "person"},
        )

        self.assertIn("car", entities)
        self.assertIn("traffic light", entities)
        self.assertIn("person", entities)

    def test_page_content_entity_fallback(self):
        labels = get_document_entity_tags(
            {},
            "Entity-focused video search document.\nEntity: traffic light.\n",
        )

        self.assertEqual({"traffic light"}, labels)

    def test_synonym_match_boosts_entity_summary(self):
        doc = DummyDocument(metadata={"tags": ["doc:entity-summary", "entity:car"]})

        reranked = apply_entity_aware_rerank([(doc, 0.50)], "show me an automobile")

        self.assertEqual(reranked[0][1], 0.70)
        self.assertEqual(doc.metadata["semantic_score"], 0.50)
        self.assertEqual(doc.metadata["entity_boost"], 0.20)
        self.assertEqual(doc.metadata["entity_match_labels"], ["car"])
        self.assertEqual(doc.metadata["reranked_score"], 0.70)

    def test_false_positive_substring_does_not_match(self):
        doc = DummyDocument(metadata={"tags": "doc:entity-summary,entity:car"})

        reranked = apply_entity_aware_rerank(
            [(doc, 0.40)], "show carpet and cartwheels"
        )

        self.assertEqual(reranked[0][1], 0.40)
        self.assertEqual(doc.metadata["entity_boost"], 0.0)
        self.assertEqual(doc.metadata["entity_match_labels"], [])

    def test_boost_is_capped(self):
        doc = DummyDocument(
            metadata={
                "tags": [
                    "doc:entity-summary",
                    "entity:car",
                    "entity:person",
                ]
            }
        )

        reranked = apply_entity_aware_rerank(
            [(doc, 0.30)],
            "car and person",
            max_boost=0.10,
            doc_type_boost=0.05,
            exact_label_boost=0.15,
        )

        self.assertEqual(reranked[0][1], 0.40)
        self.assertEqual(doc.metadata["entity_boost"], 0.10)
        self.assertEqual(doc.metadata["entity_match_labels"], ["car", "person"])

    def test_disabled_returns_original_without_metadata_mutation(self):
        doc = DummyDocument(metadata={"tags": "doc:entity-summary,entity:car"})

        reranked = apply_entity_aware_rerank(
            [(doc, 0.30)], "car", enabled=False
        )

        self.assertEqual(reranked, [(doc, 0.30)])
        self.assertNotIn("entity_boost", doc.metadata)

    def test_enabled_no_match_preserves_scores_with_zero_boost(self):
        doc = DummyDocument(metadata={"tags": "doc:entity-summary,entity:person"})

        reranked = apply_entity_aware_rerank([(doc, 0.30)], "car")

        self.assertEqual(reranked[0][1], 0.30)
        self.assertEqual(doc.metadata["semantic_score"], 0.30)
        self.assertEqual(doc.metadata["entity_boost"], 0.0)
        self.assertEqual(doc.metadata["entity_match_labels"], [])
        self.assertEqual(doc.metadata["reranked_score"], 0.30)


if __name__ == "__main__":
    unittest.main()
