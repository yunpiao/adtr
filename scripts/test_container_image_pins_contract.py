"""Offline source/pin consistency only; these tests do not pull or run images."""
from pathlib import Path
import re
import unittest

import ldap_e2e_fixture
import test_auth_e2e
import test_integration


ROOT = Path(__file__).resolve().parents[1]
# Independent expectations: importing the expected values from production would
# allow an accidental registry, tag, or digest change to silently pass.
POSTGRES = (
    "public.ecr.aws/docker/library/postgres:17.6-alpine@sha256:"
    "ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94"
)
GOLANG = (
    "public.ecr.aws/docker/library/golang:1.27.1-alpine@sha256:"
    "8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414"
)


class ContainerImagePinsContract(unittest.TestCase):
    def test_integration_uses_exact_official_postgres_pin(self):
        self.assertEqual(test_integration.IMAGE, POSTGRES)

    def test_browser_and_ldap_keep_the_shared_postgres_pin(self):
        self.assertEqual(test_auth_e2e.IMAGE, POSTGRES)
        self.assertEqual(ldap_e2e_fixture.IMAGE, POSTGRES)

    def test_compose_matches_the_exact_integration_postgres_pin(self):
        text = (ROOT / "compose.yaml").read_text()
        images = re.findall(r"^\s*image:\s*(\S+)\s*$", text, re.MULTILINE)
        self.assertEqual(images, [POSTGRES])
        self.assertEqual(images, [test_integration.IMAGE])

    def test_dockerfile_keeps_exact_go_pin_and_scratch_runtime(self):
        text = (ROOT / "Dockerfile").read_text()
        stages = [line.strip() for line in text.splitlines()
                  if re.match(r"^\s*FROM\s", line, re.IGNORECASE)]
        self.assertEqual(stages, [f"FROM {GOLANG} AS build", "FROM scratch"])


if __name__ == "__main__":
    unittest.main()
