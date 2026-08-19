"""VIGIL SDK initialization and OpenTelemetry instrumentation.

Emits spans with GenAI semantic conventions as defined by OpenTelemetry:
- https://opentelemetry.io/docs/specs/semconv/gen-ai/
"""

import logging
import os

from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter


logger = logging.getLogger(__name__)

# GenAI semantic convention constants
GEN_AI_SYSTEM = "gen_ai.system"
GEN_AI_REQUEST_MODEL = "gen_ai.request.model"
GEN_AI_REQUEST_MAX_TOKENS = "gen_ai.request.max_tokens"
GEN_AI_REQUEST_TEMPERATURE = "gen_ai.request.temperature"
GEN_AI_RESPONSE_ID = "gen_ai.response.id"
GEN_AI_USAGE_PROMPT_TOKENS = "gen_ai.usage.prompt_tokens"
GEN_AI_USAGE_COMPLETION_TOKENS = "gen_ai.usage.completion_tokens"
GEN_AI_USAGE_TOTAL_TOKENS = "gen_ai.usage.total_tokens"

# VIGIL custom attributes (namespaced under vigil.*)
VIGIL_AGENT_ID = "vigil.agent.id"
VIGIL_AGENT_PROJECT = "vigil.agent.project"
VIGIL_EXECUTION_COST = "vigil.execution.cost"
VIGIL_EXECUTION_SEQUENCE = "vigil.execution.sequence"
VIGIL_GOVERNANCE_ACTION = "vigil.governance.action"
VIGIL_GOVERNANCE_REASON = "vigil.governance.reason"
VIGIL_BUDGET_LIMIT = "vigil.budget_limit"


def init(
    agent_id: str = "default-agent",
    control_plane_url: str = "ws://localhost:8080/api/v1/vigil/agent-ws",
    api_key: str | None = None,
    auto_instrument: bool = True,
    telemetry_endpoint: str = "http://localhost:4318",
    service_name: str | None = None,
):
    """Initialize VIGIL SDK with OpenTelemetry instrumentation.

    Configures the TracerProvider with an OTLP exporter and optionally
    auto-instruments supported AI frameworks.

    Args:
        agent_id: Unique identifier for this agent.
        control_plane_url: WebSocket URL for the VIGIL Control Plane.
        api_key: Optional API key for authentication.
        auto_instrument: Whether to auto-instrument supported frameworks.
        telemetry_endpoint: OTLP HTTP/gRPC endpoint for span export.
        service_name: Override for the OTel service name (defaults to agent_id).
    """
    service = service_name or agent_id

    # Configure Resource with service identity
    resource = Resource.create({
        "service.name": service,
        "service.version": os.environ.get("VIGIL_SDK_VERSION", "0.1.0"),
        VIGIL_AGENT_ID: agent_id,
        VIGIL_BUDGET_LIMIT: os.environ.get("VIGIL_BUDGET_LIMIT", "0"),
    })

    # Set up the Tracer Provider
    provider = TracerProvider(resource=resource)

    # Configure the OTLP Exporter (HTTP/protobuf by default)
    otlp_exporter = OTLPSpanExporter(
        endpoint=telemetry_endpoint,
        insecure=True,
    )
    processor = BatchSpanProcessor(otlp_exporter)
    provider.add_span_processor(processor)

    trace.set_tracer_provider(provider)

    if auto_instrument:
        _instrument_frameworks()

    logger.info(
        "VIGIL SDK initialized",
        extra={
            "agent_id": agent_id,
            "service": service,
            "endpoint": telemetry_endpoint,
            "control_plane": control_plane_url,
        },
    )


def _instrument_frameworks():
    """Auto-instrument supported AI frameworks if installed."""
    # OpenAI
    try:
        from openinference.instrumentation.openai import OpenAIInstrumentor

        OpenAIInstrumentor().instrument()
        logger.debug("Instrumented OpenAI")
    except ImportError:
        pass

    # LangChain
    try:
        from openinference.instrumentation.langchain import LangChainInstrumentor

        LangChainInstrumentor().instrument()
        logger.debug("Instrumented LangChain")
    except ImportError:
        pass

    # LlamaIndex
    try:
        from openinference.instrumentation.llamaindex import LlamaIndexInstrumentor

        LlamaIndexInstrumentor().instrument()
        logger.debug("Instrumented LlamaIndex")
    except ImportError:
        pass

    # CrewAI
    try:
        from openinference.instrumentation.crewai import CrewAIInstrumentor

        CrewAIInstrumentor().instrument()
        logger.debug("Instrumented CrewAI")
    except ImportError:
        pass
