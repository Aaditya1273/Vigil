import os
import unittest
from unittest.mock import patch, MagicMock

from opentelemetry import trace
import vigil

class TestInstrumentation(unittest.TestCase):

    @patch('vigil.instrumentation.OTLPSpanExporter')
    @patch('vigil.instrumentation.BatchSpanProcessor')
    @patch('vigil.instrumentation._instrument_frameworks')
    def test_init_configures_tracer(self, mock_instrument_frameworks, mock_bsp, mock_exporter):
        # Keyword names must match init()'s real signature. This test used to
        # call init(project_name=, endpoint=, budget_limit=), none of which
        # exist, so it raised TypeError and had never passed.
        with patch.dict(os.environ, {"VIGIL_BUDGET_LIMIT": "5.0"}):
            vigil.init(
                agent_id="test-agent",
                telemetry_endpoint="http://localhost:4318",
                service_name="test-project",
            )

        # Verify the tracer provider is set globally
        provider = trace.get_tracer_provider()
        self.assertIsNotNone(provider)

        # Verify resource attributes are set correctly
        resource = provider.resource
        self.assertEqual(resource.attributes.get("service.name"), "test-project")
        self.assertEqual(resource.attributes.get("vigil.agent.id"), "test-agent")
        self.assertEqual(resource.attributes.get("vigil.budget_limit"), "5.0")

        # Verify framework instrumentation was called
        mock_instrument_frameworks.assert_called_once()
        
    @patch('vigil.instrumentation.logger')
    def test_instrument_frameworks_handles_missing_packages(self, mock_logger):
        # We don't have the instrumentors installed in this pure environment,
        # so this should just pass without raising ImportError.
        vigil.instrumentation._instrument_frameworks()
        # Since it passes, the function is safe.

if __name__ == '__main__':
    unittest.main()
