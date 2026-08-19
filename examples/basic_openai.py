#!/usr/bin/env python3
"""
Example: Basic OpenAI Agent with VIGIL Governance

This example demonstrates how to instrument a simple OpenAI agent
with VIGIL for cost tracking, governance, and self-healing.

Prerequisites:
    pip install vigil-sdk[openai]
    export OPENAI_API_KEY=sk-...

Usage:
    python examples/basic_openai.py

    Ensure the VIGIL Control Plane is running:
    cd demo && docker compose -f docker-compose.demo.yaml up
"""

import os

from openai import OpenAI

from vigil_sdk import init

# Initialize VIGIL - one line enables governance
init(
    agent_id="demo-agent-v1",
    control_plane_url=os.getenv(
        "VIGIL_CONTROL_PLANE_URL",
        "ws://localhost:8080/api/v1/vigil/agent-ws",
    ),
)

client = OpenAI()

response = client.chat.completions.create(
    model="gpt-3.5-turbo",
    messages=[
        {"role": "system", "content": "You are a helpful assistant."},
        {"role": "user", "content": "What is the capital of France?"},
    ],
    max_tokens=100,
)

print(f"Response: {response.choices[0].message.content}")
print(f"Tokens used: {response.usage.total_tokens}")
print(f"Trace emitted to VIGIL Control Plane ✓")
