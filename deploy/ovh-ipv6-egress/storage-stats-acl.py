#!/usr/bin/env python3
"""Grant the Sub2API UID metadata-only traversal of the IPv6 state directory.

No files are opened or chmodded. This also restores the ACL mask after systemd
applies StateDirectoryMode on a later daemon start. libacl is part of Debian's
base runtime; the setfacl CLI is not required.
"""
import ctypes
import os
from pathlib import Path

STATE = Path('/var/lib/sub2api-ovh-ipv6-egress')
APP_UID = 1000
assert os.geteuid() == 0 and STATE.is_dir() and not STATE.is_symlink()
lib = ctypes.CDLL('libacl.so.1', use_errno=True)
lib.acl_get_file.argtypes = [ctypes.c_char_p, ctypes.c_int]
lib.acl_get_file.restype = ctypes.c_void_p
lib.acl_to_text.argtypes = [ctypes.c_void_p, ctypes.c_void_p]
lib.acl_to_text.restype = ctypes.c_void_p
lib.acl_from_text.argtypes = [ctypes.c_char_p]
lib.acl_from_text.restype = ctypes.c_void_p
lib.acl_set_file.argtypes = [ctypes.c_char_p, ctypes.c_int, ctypes.c_void_p]
lib.acl_free.argtypes = [ctypes.c_void_p]
path = os.fsencode(STATE)
acl = lib.acl_get_file(path, 0x8000)
assert acl, 'Cannot read state directory ACL'
text = lib.acl_to_text(acl, None)
assert text, 'Cannot inspect state directory ACL'
try:
    rows = ctypes.string_at(text).decode().splitlines()
finally:
    lib.acl_free(text)
    lib.acl_free(acl)
entries = {}
for row in rows:
    principal, permissions = row.split('\t', 1)[0].rsplit(':', 1)
    entries[principal] = permissions
key = 'user:' + str(APP_UID)
assert entries.get(key, '--x') == '--x', 'Refuse to replace an existing different UID grant'
entries[key] = '--x'
mask = entries.get('mask:', entries['group:'])
entries['mask:'] = mask[:2] + 'x'
desired = '\n'.join(k + ':' + v for k, v in entries.items()) + '\n'
acl = lib.acl_from_text(desired.encode())
assert acl, 'Cannot construct state directory ACL'
try:
    if lib.acl_set_file(path, 0x8000, acl) != 0:
        raise OSError(ctypes.get_errno(), 'Cannot apply state directory ACL')
finally:
    lib.acl_free(acl)
