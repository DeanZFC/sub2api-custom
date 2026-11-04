#!/usr/bin/env python3
"""Excel authorization worker adapted from ranxi2001/sub2api 981f3645.

Only Password/TOTP Excel jobs are accepted. Primary Codex credentials, SIWC,
Session export, external login services and 2FA enrollment are outside this API.
Use TOSUB2_ROOT with the pinned runtime source described in reauth-runtime/README.md.
"""
from __future__ import annotations
import argparse
import base64
import codecs
import multiprocessing
import uuid
import html
import ipaddress
import json
import logging
import os
import re
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass, replace
from datetime import datetime, timezone
from html.parser import HTMLParser
from pathlib import Path
from types import SimpleNamespace
from typing import Any, Callable
from urllib.error import HTTPError, URLError
from urllib.parse import urlparse
from urllib.request import HTTPRedirectHandler, Request, build_opener
LOGGER = logging.getLogger('openai_reauth_worker')
MAX_RESPONSE_BYTES = 1048576
OPERATION_CODES = {'phone_verification_required': 'phone_verification_required', 'phone_number_required': 'phone_verification_required', 'account_deactivated': 'account_deactivated', 'account_disabled': 'account_deactivated', 'account_banned': 'account_deactivated', 'account_deleted': 'account_deleted', 'account_retired': 'account_retired', 'auth_failed': 'requires_relogin', 'invalid_token': 'requires_relogin', 'token_invalid': 'requires_relogin', 'requires_relogin': 'requires_relogin', 'invalid_grant': 'requires_relogin', 'revoked': 'requires_relogin', 'token_invalidated': 'requires_relogin', 'token_revoked': 'requires_relogin', 'refresh_token_reused': 'requires_relogin', 'refresh_token_invalidated': 'requires_relogin', 'invalid_refresh_token': 'requires_relogin', 'token_expired': 'requires_relogin', 'app_session_terminated': 'requires_relogin', 'session_unavailable': 'requires_relogin', 'password_rejected': 'password_rejected', 'invalid_password': 'password_rejected', 'incorrect_password': 'password_rejected', 'totp_rejected': 'mfa_required', 'mfa_required': 'mfa_required', 'otp_required': 'mfa_required', 'email_verification_required': 'mfa_required', 'interaction_required': 'mfa_required', 'security_check': 'security_check', 'security_check_required': 'security_check', 'challenge_required': 'security_check', 'session_blocked_by_cyber_policy': 'security_check', 'cyber_policy': 'security_check', 'access_denied': 'authorization_denied', 'authorization_denied': 'authorization_denied', 'permission_denied': 'authorization_denied', 'rate_limit_exceeded': 'rate_limited', 'rate_limit_error': 'rate_limited', 'rate_limited': 'rate_limited', 'too_many_requests': 'rate_limited', 'timeout': 'timeout', 'request_timeout': 'timeout', 'worker_timeout': 'timeout', 'idle_timeout': 'timeout', 'transport_error': 'transport_error', 'connection_error': 'transport_error', 'proxy_unavailable': 'transport_error', 'server_error': 'service_error', 'service_unavailable': 'service_error', 'service_error': 'service_error'}
OPERATION_TEXTS = {'You must provide a phone number to continue.': 'phone_verification_required', 'Phone verification required.': 'phone_verification_required', 'Your account has been deactivated.': 'account_deactivated', 'Your account has been deleted.': 'account_deleted', 'Your account has been deleted or deactivated.': 'account_retired', 'This account has been deleted or deactivated.': 'account_retired'}

def credential_operation_reason(payload):
    error = payload.get('error') if isinstance(payload, dict) else None
    if isinstance(error, str):
        return OPERATION_CODES.get(error, 'unknown')
    if not isinstance(error, dict):
        return 'unknown'
    if 'code' in error:
        code = error['code']
        return OPERATION_CODES.get(code, 'unknown') if isinstance(code, str) else 'unknown'
    kind = error.get('type')
    if isinstance(kind, str) and kind in OPERATION_CODES:
        return OPERATION_CODES[kind]
    message = error.get('message')
    return OPERATION_TEXTS.get(message, 'unknown') if isinstance(message, str) else 'unknown'

class WorkerError(RuntimeError):
    pass

class OperationWorkerError(WorkerError):
    """Created only from this adapter's validated response envelope."""

    def __init__(self, code: str):
        self.code = code if code in OPERATION_CODES.values() else 'unknown'
        super().__init__(self.code)

class _NoRedirect(HTTPRedirectHandler):

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None
NO_REDIRECT_OPENER = build_opener(_NoRedirect)

def _positive_float(raw: str | None, default: float) -> float:
    try:
        value = float(str(raw or '').strip())
    except ValueError:
        return default
    return value if value > 0 else default

@dataclass(frozen=True)
class WorkerConfig:
    base_url: str
    worker_token: str
    worker_id: str
    protocol_root: Path | None
    tosub2_root: Path | None = None
    poll_seconds: float = 5.0
    request_timeout: float = 30.0
    concurrency: int = 3

    @classmethod
    def from_env(cls) -> 'WorkerConfig':
        base_url = os.getenv('SUB2API_BASE_URL', 'http://127.0.0.1:8080').strip().rstrip('/')
        parsed = urlparse(base_url)
        host = (parsed.hostname or '').lower()
        if parsed.scheme not in {'http', 'https'} or not host or parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise WorkerError('SUB2API_BASE_URL must be an absolute HTTP(S) URL')
        if parsed.scheme == 'http' and host not in {'localhost', '127.0.0.1', '::1'}:
            raise WorkerError('SUB2API_BASE_URL must use HTTPS unless it targets localhost')
        worker_token = os.getenv('OPENAI_REAUTH_WORKER_TOKEN', '').strip()
        if len(worker_token) < 32:
            raise WorkerError('OPENAI_REAUTH_WORKER_TOKEN must contain at least 32 characters')
        worker_id = os.getenv('OPENAI_REAUTH_WORKER_ID', '').strip()
        if not worker_id:
            worker_id = f'{socket.gethostname()}-{os.getpid()}'
        if len(worker_id) > 128:
            raise WorkerError('OPENAI_REAUTH_WORKER_ID must be at most 128 characters')
        protocol_root = None
        tosub2_raw = os.getenv('TOSUB2_ROOT', '').strip()
        tosub2_root = Path(tosub2_raw).expanduser().resolve() if tosub2_raw else None
        if tosub2_root is not None:
            required_tosub2 = (tosub2_root / 'src' / 'protocol-login.mjs', tosub2_root / 'src' / 'tls-transport.mjs')
            if not all((path.is_file() for path in required_tosub2)):
                raise WorkerError('TOSUB2_ROOT must point to a toSub2 checkout containing src/protocol-login.mjs')
        if tosub2_root is None:
            raise WorkerError('configure TOSUB2_ROOT for Excel password/TOTP authorization')
        try:
            concurrency = int(os.getenv('OPENAI_REAUTH_CONCURRENCY', '3'))
        except ValueError:
            raise WorkerError('OPENAI_REAUTH_CONCURRENCY must be an integer from 1 to 16') from None
        if not 1 <= concurrency <= 16:
            raise WorkerError('OPENAI_REAUTH_CONCURRENCY must be an integer from 1 to 16')
        return cls(base_url=base_url, worker_token=worker_token, worker_id=worker_id, protocol_root=protocol_root, tosub2_root=tosub2_root, poll_seconds=_positive_float(os.getenv('OPENAI_REAUTH_POLL_SECONDS'), 5.0), request_timeout=_positive_float(os.getenv('OPENAI_REAUTH_REQUEST_TIMEOUT'), 30.0), concurrency=concurrency)

class WorkerAPI:

    def __init__(self, config: WorkerConfig):
        self.config = config

    def _post(self, path: str, payload: dict[str, Any]) -> Any:
        request = Request(self.config.base_url + path, data=json.dumps(payload).encode('utf-8'), method='POST', headers={'Content-Type': 'application/json', 'Accept': 'application/json', 'X-OpenAI-Reauth-Worker-Token': self.config.worker_token})
        try:
            with NO_REDIRECT_OPENER.open(request, timeout=self.config.request_timeout) as response:
                body = response.read(MAX_RESPONSE_BYTES + 1)
        except HTTPError as exc:
            message = ''
            try:
                parsed = json.loads(exc.read(MAX_RESPONSE_BYTES).decode('utf-8', errors='replace'))
                message = str(parsed.get('message') or parsed.get('reason') or '')
            except (ValueError, AttributeError):
                pass
            raise WorkerError(f"sub2api HTTP {exc.code}: {message or 'request failed'}") from None
        except (URLError, TimeoutError, OSError) as exc:
            raise WorkerError(f'sub2api request failed: {type(exc).__name__}') from None
        if len(body) > MAX_RESPONSE_BYTES:
            raise WorkerError('sub2api response is too large')
        try:
            envelope = json.loads(body.decode('utf-8'))
        except (UnicodeDecodeError, ValueError):
            raise WorkerError('sub2api returned invalid JSON') from None
        if not isinstance(envelope, dict) or envelope.get('code') != 0:
            raise WorkerError(str(envelope.get('message') or 'sub2api request failed'))
        return envelope.get('data')

    def runtime_concurrency(self) -> int:
        data = self._post('/api/v1/internal/openai-reauth/runtime-settings', {})
        value = data.get('worker_concurrency') if isinstance(data, dict) else None
        if isinstance(data, dict) and 'worker_concurrency' in data and (value is None):
            return self.config.concurrency
        if type(value) is not int or not 1 <= value <= 16:
            raise WorkerError('invalid worker concurrency from server')
        return value

    def claim(self) -> dict[str, Any] | None:
        data = self._post('/api/v1/internal/openai-reauth/claim', {'worker_id': self.config.worker_id, 'engines': ['local_worker']})
        return data if isinstance(data, dict) else None

    def progress(self, task_id: int, stage: str) -> None:
        self._post(f'/api/v1/internal/openai-reauth/{task_id}/progress', {'worker_id': self.config.worker_id, 'stage': stage})

    def credentials(self, task_id: int, credentials: dict[str, Any], extra: dict[str, Any] | None=None) -> dict[str, Any] | None:
        data = self._post(f'/api/v1/internal/openai-reauth/{task_id}/credentials', {'worker_id': self.config.worker_id, 'credentials': credentials, 'extra': extra or {}})
        return data if isinstance(data, dict) else None

    def fail(self, task_id: int, reason: str) -> None:
        self._post(f'/api/v1/internal/openai-reauth/{task_id}/fail', {'worker_id': self.config.worker_id, 'reason': safe_worker_failure(WorkerError(reason))})

def _best_effort_progress(api: WorkerAPI, task_id: int, stage: str) -> None:
    try:
        api.progress(task_id, stage)
    except WorkerError as exc:
        LOGGER.warning('task=%s progress=%s failed: %s', task_id, stage, safe_worker_failure(exc))

def process_password_claim(api: WorkerAPI, claim: dict[str, Any]) -> None:
    oauth_profile = str(claim.get('oauth_profile') or '')
    if oauth_profile != 'excel' or claim.get('credential_mode') != 'password_totp':
        raise WorkerError('unsupported Excel authorization profile')
    if str(claim.get('engine') or 'local_worker') != 'local_worker':
        raise WorkerError('unsupported Excel authorization engine')
    task_id = int(claim.get('task_id') or 0)
    account_id = int(claim.get('account_id') or 0)
    email = str(claim.get('login_email') or '').strip()
    password = str(claim.get('password') or '')
    totp_secret = str(claim.get('totp_secret') or '').strip()
    proxy_url = str(claim.get('proxy_url') or '').strip()
    root = getattr(getattr(api, 'config', None), 'tosub2_root', None)
    if task_id <= 0 or account_id <= 0 or (not email) or (not password):
        raise WorkerError('claimed password task is missing required fields')
    if root is None:
        raise WorkerError('TOSUB2_ROOT is required for password/TOTP re-login')
    node_executable = os.getenv('NODE_EXECUTABLE', 'node').strip()
    if not node_executable or shutil.which(node_executable) is None:
        raise WorkerError('Node.js is required for password/TOTP re-login')
    script = root.resolve() / 'src' / 'protocol-login.mjs'
    if not script.is_file():
        raise WorkerError('toSub2 protocol-login.mjs is unavailable')
    LOGGER.info('task=%s account=%s started (password protocol)', task_id, account_id)
    _best_effort_progress(api, task_id, 'starting')
    _best_effort_progress(api, task_id, 'protocol_connecting')
    with tempfile.TemporaryDirectory(prefix='sub2api-reauth-') as temp_dir:
        output_path = Path(temp_dir) / 'oauth.json'
        diagnostic_path = Path(temp_dir) / 'protocol-diagnostic.json'
        try:
            from .openai_excel_oauth_adapter import prepare_excel_runtime
        except ImportError:
            from openai_excel_oauth_adapter import prepare_excel_runtime
        try:
            script = prepare_excel_runtime(root.resolve(), Path(temp_dir))
        except ValueError as exc:
            raise WorkerError(str(exc)) from None
        command = [node_executable, str(script), '--email', email, '--output-mode', 'sub2api', '--sub2api-out', str(output_path), '--sub2api-name', f'credential-guard-{account_id}']
        if proxy_url:
            command.extend(('--proxy', proxy_url))
        child_env = os.environ.copy()
        child_env.pop('OPENAI_REAUTH_WORKER_TOKEN', None)
        for key in ('SUB2API_PROTOCOL_DIAGNOSTIC_PATH', 'SUB2API_PROTOCOL_TASK_ID', 'OPENAI_CODEX_EXPECTED_WORKSPACE'):
            child_env.pop(key, None)
        child_env['CHATGPT_PROXY_MAX_ATTEMPTS'] = '1'
        child_env.pop('CHATGPT_BASE', None)
        child_env.pop('AUTH_BASE', None)
        child_env.pop('CHATGPT_PROXY_URL', None)
        child_env.pop('OPENAI_EXCEL_EXPECTED_WORKSPACE', None)
        if claim.get('expected_workspace'):
            child_env['OPENAI_EXCEL_EXPECTED_WORKSPACE'] = str(claim['expected_workspace'])
        child_env.pop('OPENAI_SIWC_AUTH_URL', None)
        child_env.pop('OPENAI_SIWC_EXPECTED_WORKSPACE', None)
        child_env['CHATGPT_LOGIN_PASSWORD'] = password
        if totp_secret:
            child_env['CHATGPT_TOTP_SECRET'] = totp_secret
        else:
            child_env.pop('CHATGPT_TOTP_SECRET', None)
        protocol_started = time.monotonic()
        try:
            result = subprocess.run(command, cwd=temp_dir, env=child_env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=10 * 60, check=False)
        except subprocess.TimeoutExpired:
            raise WorkerError('password/TOTP protocol timed out') from None
        except OSError:
            raise WorkerError('password/TOTP protocol could not start') from None
        diagnostic = None
        if oauth_profile == 'excel' and callable(getattr(api, 'private_protocol_diagnostics', None)):
            api.private_protocol_diagnostics(result.stdout, result.stderr)
        if result.returncode != 0:
            private_output = result.stdout + '\n' + result.stderr
            stage = 'web_login'
            for marker, label in (('Password accepted', 'password_accepted'), ('TOTP 2FA challenge reached', 'mfa_challenge'), ('2FA verification accepted', 'mfa_accepted'), ('Start Codex OAuth flow', 'excel_authorize'), ('Convert OAuth callback', 'excel_token_exchange')):
                if marker in private_output:
                    stage = label
            reasons = [label for marker, label in (('security-check', 'security_check'), ('PROXY_RISK_CONTROL', 'security_check'), ('EXCEL_ADDITIONAL_VERIFICATION_REQUIRED', 'additional_verification'), ('EXCEL_CALLBACK_STATE_MISMATCH', 'callback_state_mismatch'), ('EXCEL_EXPECTED_WORKSPACE_NOT_LISTED', 'workspace_not_listed'), ('Password was rejected', 'password_rejected'), ('2FA key was rejected', 'totp_rejected'), ('SESSION_SELECTION_INVALID', 'session_selection'), ('CLIENT_ID', 'client_id')) if marker in private_output]
            reason = ','.join(sorted(set(reasons))) or 'protocol_error'
            raise WorkerError(f'Excel password/TOTP login failed (stage={stage}, reason={reason}, exit={result.returncode})')
            private_output = (result.stdout or '') + '\n' + (result.stderr or '')
            if proxy_url and any((marker in private_output.lower() for marker in ('econnrefused', 'connection refused', 'proxy connect', 'proxy error', 'could not connect', 'connect etimedout', 'socket hang up'))):
                raise WorkerError('proxy_unavailable')
            if 'Password was rejected' in private_output:
                raise WorkerError('password_rejected')
            if '2FA key was rejected' in private_output or 'code generated from the configured 2FA key was rejected' in private_output:
                raise WorkerError('totp_rejected')
            raise WorkerError(f'password/TOTP protocol failed (exit {result.returncode})')
        try:
            with output_path.open('rb') as output:
                raw_output = output.read(MAX_RESPONSE_BYTES + 1)
            if len(raw_output) > MAX_RESPONSE_BYTES:
                raise ValueError
            payload = json.loads(raw_output)
        except (OSError, UnicodeDecodeError, ValueError):
            raise WorkerError('password/TOTP protocol did not produce valid OAuth credentials') from None
    accounts = payload.get('accounts') if isinstance(payload, dict) else None
    account = accounts[0] if isinstance(accounts, list) and len(accounts) == 1 else None
    credentials = account.get('credentials') if isinstance(account, dict) else None
    extra = account.get('extra') if isinstance(account, dict) else None
    if not isinstance(credentials, dict) or not all((isinstance(credentials.get(key), str) and credentials[key].strip() for key in ('access_token', 'refresh_token', 'id_token'))):
        raise WorkerError('password/TOTP protocol returned incomplete OAuth credentials')
    try:
        from .openai_excel_oauth_adapter import validate_excel_credentials
    except ImportError:
        from openai_excel_oauth_adapter import validate_excel_credentials
    try:
        validate_excel_credentials(credentials, email, claim.get('expected_workspace'))
    except ValueError as exc:
        raise WorkerError(str(exc)) from None
    _best_effort_progress(api, task_id, 'applying_credentials')
    result = api.credentials(task_id, credentials, extra if isinstance(extra, dict) else {})
    if not result or result.get('status') != 'succeeded':
        raise WorkerError(str((result or {}).get('error') or 'credentials were not accepted'))
    LOGGER.info('task=%s account=%s succeeded', task_id, account_id)

def terminate_worker(_signum: int, _frame: Any) -> None:
    if hasattr(os, 'getpgrp') and os.getpgrp() == os.getpid():
        try:
            os.killpg(os.getpgrp(), signal.SIGKILL)
        except OSError:
            pass
    raise SystemExit(0)

def _worker_loop(config: WorkerConfig, stop: Any=None) -> None:
    signal.signal(signal.SIGTERM, terminate_worker)
    protocol = None
    api = WorkerAPI(config)
    LOGGER.info('worker=%s ready', config.worker_id)
    while stop is None or not stop.is_set():
        if not run_once(api, protocol):
            if stop is None:
                time.sleep(config.poll_seconds)
            else:
                stop.wait(config.poll_seconds)

def worker_slot_config(config: WorkerConfig, slot: int) -> WorkerConfig:
    suffix = f'-slot-{slot}'
    return replace(config, worker_id=config.worker_id[:128 - len(suffix)] + suffix, concurrency=1)

def reconcile_worker_pool(context: Any, children: dict, config: WorkerConfig, target: int) -> None:
    for slot, (child, stop) in list(children.items()):
        if not child.is_alive():
            child.join()
            del children[slot]
            if not stop.is_set():
                raise WorkerError('a re-login worker process exited')
    for slot, (_child, stop) in children.items():
        if slot > target:
            stop.set()
    for slot in range(1, target + 1):
        if slot not in children:
            stop = context.Event()
            child = context.Process(target=_worker_loop, args=(worker_slot_config(config, slot), stop))
            child.start()
            children[slot] = (child, stop)

def run_worker_pool(config: WorkerConfig) -> None:
    context = multiprocessing.get_context('fork' if sys.platform.startswith('linux') else 'spawn')
    children = {}
    api = WorkerAPI(config)
    target = config.concurrency
    next_poll = 0.0
    try:
        while True:
            now = time.monotonic()
            if now >= next_poll:
                try:
                    target = api.runtime_concurrency()
                except WorkerError:
                    pass
                next_poll = now + 5
            reconcile_worker_pool(context, children, config, target)
            time.sleep(0.25)
    finally:
        for child, _stop in children.values():
            if child.is_alive():
                child.terminate()
        for child, _stop in children.values():
            child.join(timeout=5)
            if child.is_alive():
                child.kill()
                child.join()

def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--once', action='store_true', help='claim at most one task, then exit')
    parser.add_argument('--check', action='store_true', help='validate local configuration without claiming a task')
    args = parser.parse_args()
    logging.basicConfig(level=logging.INFO, format='%(asctime)s %(levelname)s %(message)s')
    try:
        config = WorkerConfig.from_env()
        node_executable = os.getenv('NODE_EXECUTABLE', 'node').strip()
        if not node_executable or shutil.which(node_executable) is None:
            raise WorkerError('Node.js is required for the re-login protocol runner')
        protocol = None
        api = WorkerAPI(config)
    except Exception as exc:
        LOGGER.error('worker startup failed: %s', safe_worker_failure(exc))
        return 2
    if args.check:
        LOGGER.info('worker configuration valid (password_totp=%s email_otp_url=%s)', config.tosub2_root is not None, protocol is not None)
        return 0
    signal.signal(signal.SIGTERM, terminate_worker)
    LOGGER.info('worker=%s concurrency=%s ready', config.worker_id, config.concurrency)
    try:
        if not args.once:
            run_worker_pool(config)
            return 0
        while True:
            worked = run_once(api, protocol)
            if args.once:
                return 0
            if not worked:
                time.sleep(config.poll_seconds)
    except WorkerError as exc:
        LOGGER.error('worker pool failed: %s', safe_worker_failure(exc))
        return 1
    except KeyboardInterrupt:
        LOGGER.info('worker stopped')
        return 0

def safe_worker_failure(exc: BaseException) -> str:
    raw = str(exc).strip()
    if isinstance(exc, OperationWorkerError):
        return exc.code
    if raw in OPERATION_CODES:
        return OPERATION_CODES[raw]
    if raw in OPERATION_TEXTS:
        return OPERATION_TEXTS[raw]
    match = re.fullmatch(r"Excel password/TOTP login failed \(stage=([a-z_]+), reason=([a-z_,]+), exit=(-?[0-9]{1,3})\)", raw if len(raw) <= 256 else "")
    if match:
        # Never retain arbitrary runner output, URLs, credentials or email.
        reasons = match.group(2).split(',')
        for reason in ('security_check', 'additional_verification', 'password_rejected', 'totp_rejected'):
            if reason in reasons:
                return 'security_check' if reason == 'additional_verification' else OPERATION_CODES[reason]
        return 'authorization_denied'
    if isinstance(exc, (TimeoutError,socket.timeout)) or raw == 'password/TOTP protocol timed out':
        return 'timeout'
    return 'worker_failed'

def run_once(api: WorkerAPI, protocol=None) -> bool:
    try:
        claim = api.claim()
    except WorkerError as exc:
        LOGGER.warning('claim failed: %s',safe_worker_failure(exc))
        return False
    if not claim:
        return False
    task_id = int(claim.get('task_id') or 0)
    try:
        process_password_claim(api,claim)
    except Exception as exc:
        reason=safe_worker_failure(exc)
        LOGGER.error('task=%s failed: %s',task_id or '?',reason)
        if task_id>0:
            try: api.fail(task_id,reason)
            except WorkerError as fail_exc: LOGGER.error('task=%s failure report rejected: %s',task_id,safe_worker_failure(fail_exc))
    return True

if __name__ == '__main__':
    raise SystemExit(main())
