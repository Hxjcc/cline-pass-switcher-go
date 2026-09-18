import json
import unittest

from acceptance_live import inspect_cli_evidence, inspect_replay


class EvidenceTests(unittest.TestCase):
    def setUp(self):
        self.items = [
            {"type": "file_change", "status": "completed", "changes": [{"path": "result.txt"}]},
            {"type": "command_execution", "status": "completed", "command": "Get-Content result.txt", "aggregated_output": "COBALT-742 red"},
            {"type": "agent_message", "text": "ACCEPTANCE_OK"},
            {"type": "error", "message": "Long threads and multiple compactions can cause the model to be less accurate."},
        ]
        self.observations = [{"path": "/v1/responses", "calls": ["view_image", "exec_command"], "image_parts": 1}]

    def inspect(self):
        stdout = "\n".join(json.dumps({"type": "item.completed", "item": item}) for item in self.items)
        return inspect_cli_evidence(stdout, self.observations, "COBALT-742 red", 0)

    def test_file_change_does_not_require_apply_patch_in_history(self):
        evidence = self.inspect()
        self.assertTrue(evidence["cli_ok"])
        self.assertEqual(evidence["cli_remote_compactions"], 0)
        self.assertEqual(evidence["cli_local_compaction_notices"], 1)

    def test_final_claim_and_file_alone_are_insufficient(self):
        self.items = [self.items[2]]
        self.assertFalse(self.inspect()["cli_ok"])

    def test_requires_actual_image_return(self):
        self.observations[0]["image_parts"] = 0
        self.assertFalse(self.inspect()["cli_ok"])

    def test_requires_successful_patch(self):
        self.items[0]["status"] = "failed"
        self.assertFalse(self.inspect()["cli_ok"])


class ReplayTests(unittest.TestCase):
    def inspect(self, text):
        return inspect_replay({"status": "completed", "output": [{"type": "message", "content": [{"type": "output_text", "text": text}]}]})

    def test_requires_exact_schema(self):
        report = self.inspect('{"token":"COBALT-742","color":"red","pending_task":"Add DONE"}')
        self.assertTrue(report["replay_ok"])

    def test_does_not_strip_fences_to_force_success(self):
        report = self.inspect('```json\n{"token":"COBALT-742","color":"red","pending_task":"Add DONE"}\n```')
        self.assertFalse(report["replay_ok"])
        self.assertFalse(report["replay_schema_ok"])
        self.assertTrue(report["replay_state_recall_ok"])

    def test_rejects_extra_fields_and_non_objects(self):
        for text in ['[]', 'null', '{"token":"COBALT-742","color":"red","pending_task":"DONE","extra":true}']:
            self.assertFalse(self.inspect(text)["replay_ok"])


if __name__ == "__main__":
    unittest.main()
