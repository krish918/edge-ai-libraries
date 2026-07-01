# SPDX-FileCopyrightText: (C) 2026 Intel Corporation
# SPDX-License-Identifier: Apache-2.0

import os
import subprocess
import sys
import unittest


class SettingsTest(unittest.TestCase):
    def test_empty_entity_rerank_min_score_loads_as_none(self):
        env = os.environ.copy()
        env["ENTITY_RERANK_MIN_SCORE"] = ""

        result = subprocess.run(
            [
                sys.executable,
                "-c",
                (
                    "import src.utils.common as common; "
                    "assert common.settings.ENTITY_RERANK_MIN_SCORE is None"
                ),
            ],
            env=env,
            text=True,
            capture_output=True,
            check=False,
        )

        self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
