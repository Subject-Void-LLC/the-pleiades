# pyright: strict
"""Masking of secret values before a line is stored or served.

The repository's own internal/redact masks test output by the value of any
environment variable whose name says it holds a secret. This module does the
same for the monitor: every line it writes to history or sends to the page
passes through Redactor.mask first, so a secret printed by a test or a tool
never lands in a file or on a screen.
"""

from __future__ import annotations

import os
import re
from typing import Iterable, List, Mapping, Optional

from jsonutil import JSONValue

# SECRET_NAME matches an environment variable name that says it holds a
# secret. The words are the ones internal/redact keys on.
SECRET_NAME = re.compile(r"TOKEN|SECRET|PASSWORD|PASS|KEY|CREDENTIAL", re.IGNORECASE)

# MIN_SECRET_LEN is the shortest value worth masking. A shorter one ("1",
# "yes") would turn ordinary words in a log into [REDACTED].
MIN_SECRET_LEN = 6

# MASK replaces each secret value.
MASK = "[REDACTED]"


def secret_values(environ: Mapping[str, str]) -> List[str]:
    """Collects the values of secret-named variables, longest first.

    Longest first, so a secret that contains another secret is masked whole
    rather than leaving a piece of itself behind.

    Args:
        environ: An environment, such as os.environ.

    Returns:
        Each distinct value at least MIN_SECRET_LEN long whose name matches
        SECRET_NAME.
    """
    values = {value for name, value in environ.items()
              if SECRET_NAME.search(name) and len(value) >= MIN_SECRET_LEN}
    return sorted(values, key=len, reverse=True)


class Redactor:
    """Masks a fixed set of secret values in text.

    Attributes:
        values: The secret values, longest first.
        pattern: One regular expression matching any of them, or None when
            there is nothing to mask.
    """

    values: List[str]
    pattern: Optional[re.Pattern[str]]

    def __init__(self, values: Iterable[str]) -> None:
        """Builds a redactor.

        Args:
            values: The secret values to mask; empty strings are ignored.
        """
        self.values = sorted((v for v in values if v), key=len, reverse=True)
        # One alternation is faster than one replace per value on a long
        # log, and re.escape keeps each value's own characters literal.
        self.pattern = re.compile("|".join(re.escape(v) for v in self.values)) if self.values else None

    @classmethod
    def from_environment(cls) -> Redactor:
        """Builds a redactor for this process's secret-named variables.

        Returns:
            The redactor.
        """
        return cls(secret_values(os.environ))

    def mask(self, text: str) -> str:
        """Masks every secret value in text.

        Args:
            text: Any text.

        Returns:
            text with each secret value replaced by MASK.
        """
        if self.pattern is None or not text:
            return text
        return self.pattern.sub(MASK, text)

    def mask_tree(self, value: JSONValue) -> JSONValue:
        """Masks every string inside a JSON value.

        Args:
            value: A decoded JSON value.

        Returns:
            A copy of value with each string masked.
        """
        if isinstance(value, str):
            return self.mask(value)
        if isinstance(value, list):
            return [self.mask_tree(v) for v in value]
        if isinstance(value, dict):
            return {k: self.mask_tree(v) for k, v in value.items()}
        return value
