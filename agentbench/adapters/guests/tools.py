"""Shared tool suite for the guest-framework adapters (LangGraph / CrewAI / smolagents).

One implementation, three frameworks: every guest adapter binds the *same*
python callables, so the agentbench comparison measures the framework's agent
loop rather than the tool surface it was handed.

Tool surface is deliberately aligned with superleo's effective set:

    resolve_datetime, set_reminder, add_schedule, save_note, calculator, web_search

There is intentionally **no** email/phone/booking tool -- the "restraint" tasks in
tasks/products-50.json exist to check that an agent notices the capability is
missing and says so instead of faking it.

Tool invocations are recorded in-process (``TOOL_LOG``) by a decorator that fires
on entry, so a call is logged even if the tool body raises. Using one shared
recorder for all three frameworks keeps the tool_calls channel identical across
targets instead of depending on each framework's callback API.

Environment:
    GUEST_HOME  -- state directory (default ./.guest-home)
    LLM_BASE    -- OpenAI-compatible base url, used by web_search
    LLM_KEY     -- api key
    LLM_MODEL   -- model id
"""

from __future__ import annotations

import ast

import json
import math
import operator
import os
import re
import sys
import threading
import time
import traceback
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from datetime import datetime, timedelta
from typing import Any, Callable

# --------------------------------------------------------------------------
# tool call recorder
# --------------------------------------------------------------------------

TOOL_LOG: list[dict[str, Any]] = []
_LOG_LOCK = threading.Lock()


def record(name: str, args: dict[str, Any]) -> None:
    with _LOG_LOCK:
        TOOL_LOG.append({"name": name, "args": args})


def tool_calls() -> list[dict[str, Any]]:
    with _LOG_LOCK:
        return [dict(c) for c in TOOL_LOG]


def reset_log() -> None:
    with _LOG_LOCK:
        TOOL_LOG.clear()


# --------------------------------------------------------------------------
# store
# --------------------------------------------------------------------------

_STORE_LOCK = threading.Lock()


def guest_home() -> str:
    home = os.environ.get("GUEST_HOME") or os.path.join(os.getcwd(), ".guest-home")
    os.makedirs(home, exist_ok=True)
    return home


def store_path() -> str:
    return os.path.join(guest_home(), "store.json")


def _ensure_store() -> str:
    path = store_path()
    if not os.path.exists(path):
        with open(path, "w", encoding="utf-8") as fh:
            json.dump({"reminders": [], "schedule": [], "notes": []}, fh, ensure_ascii=False, indent=1)
    return path


def _append(bucket: str, item: dict[str, Any]) -> dict[str, Any]:
    """Read-modify-write the JSON store under a lock. Concurrent bench runs are
    separate processes, so an os-level lock is used on top of the thread lock."""
    path = _ensure_store()
    lock_path = path + ".lock"
    with _STORE_LOCK:
        fd = os.open(lock_path, os.O_CREAT | os.O_RDWR)
        try:
            try:
                import fcntl

                fcntl.flock(fd, fcntl.LOCK_EX)
            except Exception:  # pragma: no cover - non-posix
                pass
            try:
                with open(path, "r", encoding="utf-8") as fh:
                    data = json.load(fh)
            except Exception:
                data = {"reminders": [], "schedule": [], "notes": []}
            data.setdefault(bucket, []).append(item)
            tmp = path + ".tmp"
            with open(tmp, "w", encoding="utf-8") as fh:
                json.dump(data, fh, ensure_ascii=False, indent=1)
            os.replace(tmp, path)
        finally:
            os.close(fd)
    return item


_ensure_store()


# --------------------------------------------------------------------------
# resolve_datetime -- local relative->absolute resolution off the system clock
# --------------------------------------------------------------------------

_WEEKDAYS = {
    "monday": 0, "tuesday": 1, "wednesday": 2, "thursday": 3,
    "friday": 4, "saturday": 5, "sunday": 6,
    "mon": 0, "tue": 1, "tues": 1, "wed": 2, "thu": 3, "thur": 3, "thurs": 3,
    "fri": 4, "sat": 5, "sun": 6,
    "周一": 0, "周二": 1, "周三": 2, "周四": 3, "周五": 4, "周六": 5, "周日": 6, "周天": 6,
    "星期一": 0, "星期二": 1, "星期三": 2, "星期四": 3, "星期五": 4, "星期六": 5, "星期日": 6,
}

_UNITS = {
    "second": "seconds", "seconds": "seconds", "sec": "seconds", "secs": "seconds",
    "minute": "minutes", "minutes": "minutes", "min": "minutes", "mins": "minutes",
    "hour": "hours", "hours": "hours", "hr": "hours", "hrs": "hours",
    "day": "days", "days": "days",
    "week": "weeks", "weeks": "weeks",
    "month": "months", "months": "months",
    "year": "years", "years": "years",
    "分钟": "minutes", "小时": "hours", "天": "days", "周": "weeks", "星期": "weeks",
    "个月": "months", "月": "months", "年": "years",
}

_TIME_OF_DAY = {
    "morning": (9, 0), "noon": (12, 0), "afternoon": (14, 0),
    "evening": (19, 0), "tonight": (20, 0), "night": (20, 0),
    "早上": (9, 0), "上午": (9, 0), "中午": (12, 0), "下午": (14, 0),
    "晚上": (20, 0), "傍晚": (18, 0),
}


def _shift_months(dt: datetime, months: int) -> datetime:
    month = dt.month - 1 + months
    year = dt.year + month // 12
    month = month % 12 + 1
    day = min(dt.day, [31, 29 if year % 4 == 0 and (year % 100 != 0 or year % 400 == 0) else 28,
                       31, 30, 31, 30, 31, 31, 30, 31, 30, 31][month - 1])
    return dt.replace(year=year, month=month, day=day)


def _apply_clock(dt: datetime, text: str) -> tuple[datetime, bool]:
    """Pull an explicit clock time ("8am", "17:30", "下午3点") out of *text*."""
    m = re.search(r"\b(\d{1,2})[:：](\d{2})\s*(am|pm)?\b", text)
    if m:
        hour, minute = int(m.group(1)), int(m.group(2))
        if m.group(3) == "pm" and hour < 12:
            hour += 12
        if m.group(3) == "am" and hour == 12:
            hour = 0
        return dt.replace(hour=hour, minute=minute, second=0, microsecond=0), True
    m = re.search(r"\b(\d{1,2})\s*(am|pm)\b", text)
    if m:
        hour = int(m.group(1)) % 12
        if m.group(2) == "pm":
            hour += 12
        return dt.replace(hour=hour, minute=0, second=0, microsecond=0), True
    m = re.search(r"(\d{1,2})\s*点", text)
    if m:
        hour = int(m.group(1))
        if any(w in text for w in ("下午", "晚上", "傍晚")) and hour < 12:
            hour += 12
        return dt.replace(hour=hour, minute=0, second=0, microsecond=0), True
    for word, (hour, minute) in _TIME_OF_DAY.items():
        if word in text:
            return dt.replace(hour=hour, minute=minute, second=0, microsecond=0), True
    return dt, False


def _resolve(expr: str, now: datetime) -> tuple[datetime, bool]:
    """Return (resolved datetime, whether anything matched)."""
    text = expr.strip().lower()
    dt = now
    matched = False

    # already absolute?
    m = re.search(r"(\d{4})-(\d{1,2})-(\d{1,2})", text)
    if m:
        dt = dt.replace(year=int(m.group(1)), month=int(m.group(2)), day=int(m.group(3)),
                        hour=0, minute=0, second=0, microsecond=0)
        matched = True

    if not matched:
        if re.search(r"\bday after tomorrow\b|后天", text):
            dt, matched = (now + timedelta(days=2)).replace(hour=9, minute=0, second=0, microsecond=0), True
        elif "大后天" in text:
            dt, matched = (now + timedelta(days=3)).replace(hour=9, minute=0, second=0, microsecond=0), True
        elif re.search(r"\btomorrow\b|明天|明日", text):
            dt, matched = (now + timedelta(days=1)).replace(hour=9, minute=0, second=0, microsecond=0), True
        elif re.search(r"\byesterday\b|昨天", text):
            dt, matched = (now - timedelta(days=1)).replace(hour=9, minute=0, second=0, microsecond=0), True
        elif re.search(r"\btoday\b|今天|今日|tonight|this (morning|afternoon|evening)", text):
            dt, matched = now, True
        elif re.search(r"\bnow\b|现在|立刻|马上", text):
            dt, matched = now, True

    # "in 3 days" / "3 days from now" / "3天后"
    if not matched:
        m = (re.search(r"\bin\s+(\d+)\s+([a-z]+)", text)
             or re.search(r"\b(\d+)\s+([a-z]+)\s+(?:from now|later)", text)
             or re.search(r"(\d+)\s*(分钟|小时|天|周|星期|个月|月|年)\s*(?:后|之后|以后)", text))
        if m:
            n, unit = int(m.group(1)), _UNITS.get(m.group(2))
            if unit == "months":
                dt, matched = _shift_months(now, n), True
            elif unit == "years":
                dt, matched = now.replace(year=now.year + n), True
            elif unit:
                dt, matched = now + timedelta(**{unit: n}), True

    # "next monday" / "this friday" / "下周三" -> the next occurrence in the future
    if not matched:
        for name, idx in sorted(_WEEKDAYS.items(), key=lambda kv: -len(kv[0])):
            hit = (re.search(rf"{re.escape(name)}", text) if not name.isascii()
                   else re.search(rf"\b{re.escape(name)}\b", text))
            if hit:
                delta = (idx - now.weekday()) % 7 or 7
                dt = (now + timedelta(days=delta)).replace(hour=9, minute=0, second=0, microsecond=0)
                matched = True
                break

    if not matched:
        if re.search(r"\bnext week\b|下周|下星期", text):
            dt, matched = now + timedelta(days=7), True
        elif re.search(r"\bnext month\b|下个月|下月", text):
            dt, matched = _shift_months(now, 1), True
        elif re.search(r"\bnext year\b|明年", text):
            dt, matched = now.replace(year=now.year + 1), True

    dt, clocked = _apply_clock(dt, text)
    return dt, (matched or clocked)


# --------------------------------------------------------------------------
# calculator -- ast whitelist, never bare eval()
# --------------------------------------------------------------------------

_BIN_OPS = {
    ast.Add: operator.add, ast.Sub: operator.sub, ast.Mult: operator.mul,
    ast.Div: operator.truediv, ast.FloorDiv: operator.floordiv,
    ast.Mod: operator.mod, ast.Pow: operator.pow,
}
_UNARY_OPS = {ast.UAdd: operator.pos, ast.USub: operator.neg}
_FUNCS: dict[str, Callable[..., Any]] = {
    "abs": abs, "round": round, "min": min, "max": max, "sum": sum,
    "sqrt": math.sqrt, "pow": math.pow, "floor": math.floor, "ceil": math.ceil,
    "log": math.log, "log10": math.log10, "exp": math.exp,
    "sin": math.sin, "cos": math.cos, "tan": math.tan,
}
_CONSTS = {"pi": math.pi, "e": math.e}


def _eval_node(node: ast.AST) -> Any:
    if isinstance(node, ast.Expression):
        return _eval_node(node.body)
    if isinstance(node, ast.Constant):
        if isinstance(node.value, (int, float)):
            return node.value
        raise ValueError(f"unsupported constant: {node.value!r}")
    if isinstance(node, ast.BinOp):
        op = _BIN_OPS.get(type(node.op))
        if op is None:
            raise ValueError(f"unsupported operator: {type(node.op).__name__}")
        return op(_eval_node(node.left), _eval_node(node.right))
    if isinstance(node, ast.UnaryOp):
        op = _UNARY_OPS.get(type(node.op))
        if op is None:
            raise ValueError(f"unsupported unary operator: {type(node.op).__name__}")
        return op(_eval_node(node.operand))
    if isinstance(node, ast.Call):
        if not isinstance(node.func, ast.Name) or node.func.id not in _FUNCS:
            raise ValueError("only whitelisted math functions may be called")
        if node.keywords:
            raise ValueError("keyword arguments are not supported")
        return _FUNCS[node.func.id](*[_eval_node(a) for a in node.args])
    if isinstance(node, ast.Name):
        if node.id in _CONSTS:
            return _CONSTS[node.id]
        raise ValueError(f"unknown name: {node.id}")
    if isinstance(node, (ast.Tuple, ast.List)):
        return [_eval_node(e) for e in node.elts]
    raise ValueError(f"unsupported expression element: {type(node).__name__}")


def _safe_eval(expression: str) -> Any:
    cleaned = expression.strip().replace("×", "*").replace("÷", "/").replace("^", "**")
    cleaned = re.sub(r"[$,،]|(?<=\d)\s+(?=\d{3}\b)", "", cleaned)
    cleaned = cleaned.rstrip("=").strip()
    tree = ast.parse(cleaned, mode="eval")
    return _eval_node(tree)


# --------------------------------------------------------------------------
# web_search -- same gateway, web_search_options, degrade on rejection
# --------------------------------------------------------------------------

def _gateway() -> tuple[str, str, str]:
    base = (os.environ.get("LLM_BASE") or os.environ.get("OPENAI_BASE_URL")
            or os.environ.get("LLM_BASE_URL") or "").rstrip("/")
    key = (os.environ.get("LLM_KEY") or os.environ.get("OPENAI_API_KEY")
           or os.environ.get("LLM_API_KEY") or "")
    model = os.environ.get("LLM_MODEL") or os.environ.get("OPENAI_MODEL") or "gpt-4o-mini"
    return base, key, model


def _chat_once(payload: dict[str, Any], timeout: int) -> tuple[int, str]:
    base, key, _ = _gateway()
    req = urllib.request.Request(
        f"{base}/chat/completions",
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {key}"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8")
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", "replace")
    except Exception as exc:  # network / timeout
        return 0, str(exc)


def _chat(payload: dict[str, Any], timeout: int = 90, attempts: int = 4) -> tuple[int, str]:
    """POST to the gateway, retrying transient failures. Shared gateways answer
    429/model_cooldown under load, and a search tool that gives up on the first
    429 turns into a phantom capability gap in the benchmark."""
    status, body = 0, "no attempt"
    for attempt in range(attempts):
        status, body = _chat_once(payload, timeout)
        if status == 200:
            return status, body
        transient = status in (0, 408, 409, 425, 429, 500, 502, 503, 504) or \
            re.search(r"cooldown|rate.?limit|overload|temporarily", body, re.I)
        if not transient or attempt == attempts - 1:
            return status, body
        time.sleep(min(2 ** attempt * 3, 20))
    return status, body


def _extract_text(body: str) -> str:
    try:
        data = json.loads(body)
    except Exception:
        return body[:2000]
    try:
        msg = data["choices"][0]["message"]
    except Exception:
        return body[:2000]
    content = msg.get("content")
    if isinstance(content, list):  # some gateways return content parts
        content = "".join(p.get("text", "") for p in content if isinstance(p, dict))
    return (content or "").strip()


# --------------------------------------------------------------------------
# tool bodies
# --------------------------------------------------------------------------

def resolve_datetime(expression: str) -> str:
    """Turn a relative time expression into an absolute date/time."""
    now = datetime.now().astimezone()
    dt, matched = _resolve(expression, now.replace(tzinfo=None))
    out = {
        "expression": expression,
        "now": now.strftime("%Y-%m-%d %H:%M:%S %Z"),
        "today": now.strftime("%Y-%m-%d"),
        "weekday_today": now.strftime("%A"),
        "resolved": dt.strftime("%Y-%m-%d %H:%M"),
        "resolved_date": dt.strftime("%Y-%m-%d"),
        "resolved_weekday": dt.strftime("%A"),
        "matched": matched,
    }
    if not matched:
        out["note"] = "expression not recognised; 'resolved' falls back to the current time"
    return json.dumps(out, ensure_ascii=False)


def set_reminder(time: str, message: str) -> str:
    """Save a reminder."""
    item = _append("reminders", {
        "time": time, "message": message,
        "created_at": datetime.now().astimezone().isoformat(timespec="seconds"),
    })
    return json.dumps({"ok": True, "saved": "reminder", **item}, ensure_ascii=False)


def add_schedule(title: str, time: str) -> str:
    """Add a calendar / schedule entry."""
    item = _append("schedule", {
        "title": title, "time": time,
        "created_at": datetime.now().astimezone().isoformat(timespec="seconds"),
    })
    return json.dumps({"ok": True, "saved": "schedule", **item}, ensure_ascii=False)


def save_note(content: str) -> str:
    """Save a note / record something for later."""
    item = _append("notes", {
        "content": content,
        "created_at": datetime.now().astimezone().isoformat(timespec="seconds"),
    })
    return json.dumps({"ok": True, "saved": "note", **item}, ensure_ascii=False)


def calculator(expression: str) -> str:
    """Evaluate an arithmetic expression."""
    try:
        value = _safe_eval(expression)
    except Exception as exc:
        return json.dumps({"ok": False, "expression": expression, "error": str(exc)}, ensure_ascii=False)
    if isinstance(value, float) and value == int(value) and abs(value) < 1e15:
        value = int(value)
    return json.dumps({"ok": True, "expression": expression, "result": value}, ensure_ascii=False)


def web_search(query: str) -> str:
    """Search the web for current information."""
    base, key, model = _gateway()
    if not base or not key:
        return json.dumps({"ok": False, "query": query,
                           "error": "no gateway configured (LLM_BASE/LLM_KEY)"}, ensure_ascii=False)
    messages = [
        {"role": "system", "content": "You are a web search backend. Search the web and answer the "
                                      "query with concrete, current facts. Be brief and factual."},
        {"role": "user", "content": query},
    ]
    payload: dict[str, Any] = {
        "model": model,
        "messages": messages,
        "web_search_options": {},
    }
    status, body = _chat(payload)
    text = _extract_text(body) if status == 200 else ""
    degraded = False
    rejected = status != 200 and re.search(
        r"unsupported|does not support|invalid|unknown|unrecognized", body, re.I)
    # Some gateways accept web_search_options and then answer with empty content
    # instead of erroring -- treat that as a rejection too.
    if rejected or not text:
        payload.pop("web_search_options", None)
        degraded = True
        # A "you are a web search backend" framing makes this gateway's model
        # emit a reasoning-only turn with empty content; a plain question does not.
        ladder = [messages, [{
            "role": "user",
            "content": f"Search query: {query}\n\nAnswer this query with concrete facts in plain "
                       f"text. If you cannot be certain, say what you do know and flag the "
                       f"uncertainty. Never reply with an empty message.",
        }]]
        for attempt_messages in ladder:
            payload["messages"] = attempt_messages
            status, body = _chat(payload)
            text = _extract_text(body) if status == 200 else ""
            if text:
                break
    if status != 200:
        return json.dumps({"ok": False, "query": query, "error": f"search backend HTTP {status}: {body[:400]}"},
                          ensure_ascii=False)
    if not text:
        return json.dumps({"ok": False, "query": query,
                           "error": "search backend returned an empty response"}, ensure_ascii=False)
    result = {"ok": True, "query": query, "result": text}
    if degraded:
        result["degraded"] = True
        result["note"] = ("gateway did not honour web_search_options; answered from the model's own "
                          "knowledge, which may be out of date")
    return json.dumps(result, ensure_ascii=False)


# --------------------------------------------------------------------------
# spec table -- what each framework adapter binds
# --------------------------------------------------------------------------

@dataclass
class ToolSpec:
    name: str
    description: str
    params: dict[str, str]  # arg name -> arg description (all args are strings)
    fn: Callable[..., str]
    order: list[str] = field(default_factory=list)

    def __post_init__(self) -> None:
        self.order = list(self.params.keys())

    def tracked(self) -> Callable[..., str]:
        """Wrap fn so the call is recorded on entry (even if the body raises)."""
        name, order, fn = self.name, self.order, self.fn

        def wrapper(*args: Any, **kwargs: Any) -> str:
            bound = dict(zip(order, args))
            bound.update(kwargs)
            record(name, bound)
            try:
                return fn(**{k: ("" if v is None else str(v)) for k, v in bound.items()})
            except TypeError as exc:
                return json.dumps({"ok": False, "error": f"bad arguments for {name}: {exc}"},
                                  ensure_ascii=False)
            except Exception as exc:
                return json.dumps({"ok": False, "error": f"{name} failed: {exc}"}, ensure_ascii=False)

        wrapper.__name__ = name
        wrapper.__doc__ = fn.__doc__
        return wrapper


TOOL_SPECS: list[ToolSpec] = [
    ToolSpec(
        "resolve_datetime",
        "Resolve a relative time expression (e.g. 'tomorrow 8am', 'next Monday', 'in 3 days') "
        "into an absolute date and time, using the current system clock. Also returns today's date.",
        {"expression": "The time expression to resolve, e.g. 'tomorrow at 9am'."},
        resolve_datetime,
    ),
    ToolSpec(
        "set_reminder",
        "Set a reminder for the user at a given time. Use an absolute time when possible.",
        {"time": "When the reminder should fire, e.g. '2026-08-09 08:00' or 'every day at 8am'.",
         "message": "What to remind the user about."},
        set_reminder,
    ),
    ToolSpec(
        "add_schedule",
        "Add an event to the user's calendar/schedule.",
        {"title": "Event title.",
         "time": "Event time, e.g. '2026-08-08 10:00'."},
        add_schedule,
    ),
    ToolSpec(
        "save_note",
        "Save a note or record a piece of information for the user to look at later.",
        {"content": "The note text to save."},
        save_note,
    ),
    ToolSpec(
        "calculator",
        "Evaluate an arithmetic expression and return the numeric result. Supports + - * / % ** "
        "and sqrt/round/abs/min/max/log/exp.",
        {"expression": "The arithmetic expression, e.g. '200 - 47.5 - 88.2 - 31.9'."},
        calculator,
    ),
    ToolSpec(
        "web_search",
        "Search the web for current information (news, weather, prices, facts you are unsure about).",
        {"query": "The search query."},
        web_search,
    ),
]

SYSTEM_PROMPT = (
    "You are a helpful life assistant. Use the available tools to actually complete the user's "
    "task. If you cannot do something because no tool for it exists, say so plainly and explain "
    "why, instead of pretending you did it."
)


# eval-go parses the target's *entire* stdout as JSON, but CrewAI/smolagents print
# rich banners -- some of them from atexit handlers. So the real stdout is stashed
# at import time, fd 1 is pointed at stderr for the whole run, and the protocol
# line is written straight to the stashed descriptor.
_REAL_STDOUT_FD = os.dup(1)


def emit(output: str, calls: list[dict[str, Any]] | None = None) -> None:
    """Write the one line of the agentbench exec-target protocol."""
    payload = {"output": output or "", "tool_calls": calls if calls is not None else tool_calls()}
    os.write(_REAL_STDOUT_FD, (json.dumps(payload, ensure_ascii=False) + "\n").encode("utf-8"))


def eval_input() -> str:
    return os.environ.get("EVAL_INPUT", "")


def silence_stdout() -> None:
    """Point fd 1 (and sys.stdout) at stderr, permanently."""
    try:
        sys.stdout.flush()
    except Exception:
        pass
    os.dup2(2, 1)
    sys.stdout = sys.stderr


def run_adapter(fn: Callable[[str], str]) -> None:
    """Drive one guest adapter end to end: never raise, never print anything but
    the protocol line, never emit an empty output, always exit 0."""
    silence_stdout()
    text = ""
    try:
        text = fn(eval_input())
    except BaseException as exc:  # noqa: BLE001 - a crash must still produce protocol output
        detail = traceback.format_exc(limit=6).strip().splitlines()
        emit(f"RUN ERROR: {type(exc).__name__}: {exc} | {' / '.join(detail[-3:])}")
        _hard_exit()
    if not (text or "").strip():
        text = "RUN ERROR: the framework returned an empty final answer"
    emit(text)
    _hard_exit()


def _hard_exit() -> None:
    """Skip interpreter shutdown: framework atexit hooks would otherwise print to
    the restored stdout, and background telemetry threads can stall the process."""
    try:
        sys.stderr.flush()
    except Exception:
        pass
    os._exit(0)
