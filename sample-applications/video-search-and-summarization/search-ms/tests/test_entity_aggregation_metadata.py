# SPDX-FileCopyrightText: (C) 2026 Intel Corporation
# SPDX-License-Identifier: Apache-2.0

import importlib
import sys
import types
import unittest


class DummyDocument:
    def __init__(self, metadata):
        self.metadata = metadata


class NoopLogger:
    def debug(self, *args, **kwargs):
        pass

    info = warning = error = debug


class FakeSettings:
    AGGREGATION_SEGMENT_DURATION = 8
    AGGREGATION_MIN_GAP = 0
    AGGREGATION_MAX_RESULTS = 20
    AGGREGATION_INITIAL_K = 1000
    AGGREGATION_ENABLED = True
    AGGREGATION_CONTEXT_SEEK_OFFSET_SECONDS = 0.0
    AGGREGATION_QUAL_MAX_WEIGHT = 0.65
    AGGREGATION_QUAL_TOP_WEIGHT = 0.35
    AGGREGATION_QUAL_TOP_RATIO = 0.35
    AGGREGATION_QUAL_TOP_MIN_COUNT = 1
    AGGREGATION_QUAL_TOP_MAX_COUNT = 6
    AGGREGATION_CONTEXT_SIGMA_SECONDS = 40.0
    AGGREGATION_CONTEXT_BOOST_STRENGTH = 0.0


def import_retriever_with_stubs():
    common_module = types.ModuleType("src.utils.common")
    common_module.settings = FakeSettings()
    common_module.logger = NoopLogger()
    sys.modules["src.utils.common"] = common_module

    embedding_module = types.ModuleType("src.vdms_retriever.embedding_wrapper")
    embedding_module.EmbeddingAPI = object
    sys.modules["src.vdms_retriever.embedding_wrapper"] = embedding_module

    vectorstores_module = types.ModuleType("langchain_vdms.vectorstores")
    vectorstores_module.VDMS = object
    vectorstores_module.VDMS_Client = object
    langchain_vdms_module = types.ModuleType("langchain_vdms")
    langchain_vdms_module.vectorstores = vectorstores_module
    sys.modules["langchain_vdms"] = langchain_vdms_module
    sys.modules["langchain_vdms.vectorstores"] = vectorstores_module
    sys.modules.pop("src.vdms_retriever.retriever", None)
    return importlib.import_module("src.vdms_retriever.retriever")


class EntityAggregationMetadataTest(unittest.TestCase):
    def test_entity_rerank_metadata_is_preserved_in_aggregation_output(self):
        retriever = import_retriever_with_stubs()
        frame = DummyDocument(
            {
                "video_id": "video-1",
                "timestamp": 2.0,
                "video_duration": 12.0,
                "relevance_score": 0.70,
                "doc_type": "entity-summary",
                "entity_match_labels": ["car"],
                "entity_boost": 0.20,
                "semantic_score": 0.50,
                "reranked_score": 0.70,
            }
        )

        results, _stats = retriever.aggregate_frame_results_to_videos(
            [frame], max_results=1
        )

        best_frame_info = results[0]["best_frame_info"]
        score_breakdown = results[0]["score_breakdown"]
        for key, expected in {
            "doc_type": "entity-summary",
            "entity_match_labels": ["car"],
            "entity_boost": 0.20,
            "semantic_score": 0.50,
            "reranked_score": 0.70,
        }.items():
            self.assertEqual(best_frame_info[key], expected)
            self.assertEqual(score_breakdown[key], expected)


if __name__ == "__main__":
    unittest.main()
