# pyright: strict
"""Typed access to decoded JSON, so no module needs Any.

json.load returns values whose type is only known at run time. This module
names that type (JSONValue) and gives small readers that check a value's
type before handing it on, so a record with a missing or odd field yields a
default instead of an exception.
"""

from __future__ import annotations

import json
from typing import Dict, List, Optional, Union

JSONValue = Union[Dict[str, "JSONValue"], List["JSONValue"], str, int, float, bool, None]
JSONObject = Dict[str, JSONValue]


def loads(text: str) -> JSONValue:
    """Decodes JSON text.

    Args:
        text: The JSON document.

    Returns:
        The decoded value.

    Raises:
        ValueError: The text is not JSON.
    """
    value: JSONValue = json.loads(text)
    return value


def as_object(value: JSONValue) -> Optional[JSONObject]:
    """Returns value when it is a JSON object.

    Args:
        value: Any decoded JSON value.

    Returns:
        The object, or None when value is not one.
    """
    return value if isinstance(value, dict) else None


def get_str(obj: Optional[JSONObject], key: str, default: str = "") -> str:
    """Reads a string field.

    Args:
        obj: The object to read, or None.
        key: The field name.
        default: What to return when the field is missing or not a string.

    Returns:
        The field's value or default.
    """
    if obj is None:
        return default
    value = obj.get(key)
    return value if isinstance(value, str) else default


def get_float(obj: Optional[JSONObject], key: str) -> Optional[float]:
    """Reads a number field as a float.

    Args:
        obj: The object to read, or None.
        key: The field name.

    Returns:
        The number, or None when it is missing or not a number.
    """
    if obj is None:
        return None
    value = obj.get(key)
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    return float(value)


def get_int(obj: Optional[JSONObject], key: str) -> Optional[int]:
    """Reads an integer field.

    Args:
        obj: The object to read, or None.
        key: The field name.

    Returns:
        The integer, or None when it is missing or not an integer.
    """
    if obj is None:
        return None
    value = obj.get(key)
    if isinstance(value, bool) or not isinstance(value, int):
        return None
    return value


def get_list(obj: Optional[JSONObject], key: str) -> List[JSONValue]:
    """Reads a list field.

    Args:
        obj: The object to read, or None.
        key: The field name.

    Returns:
        The list, or an empty list when it is missing or not a list.
    """
    if obj is None:
        return []
    value = obj.get(key)
    return value if isinstance(value, list) else []


def get_objects(obj: Optional[JSONObject], key: str) -> List[JSONObject]:
    """Reads a list field, keeping only its objects.

    Args:
        obj: The object to read, or None.
        key: The field name.

    Returns:
        The objects in the list, in order.
    """
    out: List[JSONObject] = []
    for item in get_list(obj, key):
        if isinstance(item, dict):
            out.append(item)
    return out


def get_strings(obj: Optional[JSONObject], key: str) -> List[str]:
    """Reads a list field, keeping only its strings.

    Args:
        obj: The object to read, or None.
        key: The field name.

    Returns:
        The strings in the list, in order.
    """
    return [item for item in get_list(obj, key) if isinstance(item, str)]
