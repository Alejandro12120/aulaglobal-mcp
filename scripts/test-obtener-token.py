"""Offline interactive tests: docker run --rm -v "$PWD":/src -w /src python:3.12 python scripts/test-obtener-token.py"""
import os, pty, select, shutil, tempfile, time
from pathlib import Path

SCRIPT = Path('obtener-token.sh').resolve()

def run(name, replies, existing=False, fail='', verify='1', curl='', missing=False):
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        shutil.copy(SCRIPT, root / SCRIPT.name)
        stub = root / 'stub'
        stub.write_text('''#!/usr/bin/env bash
set -eu
value=$(cat)
[[ "$value" == 'abc123' ]] || exit 9
[[ "$*" == *'token-from-cookie --output secrets/token'* ]] || exit 8
if [[ -n "${FAIL:-}" ]]; then echo "$FAIL" >&2; exit 1; fi
if [[ -e secrets/token ]]; then [[ "$*" == *--force* ]] || exit 7; fi
printf fake > secrets/token
printf private > secrets/token.private
printf '{"obtained_at":"2026-11-14T12:00:00Z"}' > secrets/token.meta.json
chmod 600 secrets/token*
''')
        stub.chmod(0o700)
        if existing:
            (root / 'secrets').mkdir()
            (root / 'secrets/token').write_text('old')
            (root / 'secrets/token.meta.json').write_text('{"obtained_at":"2020-01-01"}')
        if missing:
            docker = root / 'docker'
            docker.write_text('#!/bin/sh\ncp stub aulaglobal-mcp\nchmod 700 aulaglobal-mcp\n')
            docker.chmod(0o700)
        curlstub = root / 'curl'
        curlstub.write_text('#!/bin/sh\ncase "$*" in *wstoken@secrets/token*) ;; *) exit 9;; esac\nprintf \'%s\' \'' + curl + "'\n")
        curlstub.chmod(0o700)
        pid, fd = pty.fork()
        if pid == 0:
            os.chdir(root)
            os.environ.update(AULAGLOBAL_MCP_BIN=str(root/'missing') if missing else str(stub), AULAGLOBAL_SKIP_VERIFY=verify, FAIL=fail, PATH=tmp+':'+os.environ['PATH'])
            os.execl('/bin/bash', 'bash', './obtener-token.sh')
        output = b''
        deadline = time.monotonic()+10
        for prompt, answer in replies:
            while prompt.encode() not in output:
                assert time.monotonic() < deadline, (name, output)
                if select.select([fd], [], [], .1)[0]:
                    output += os.read(fd, 65536)
            time.sleep(.05)  # let read disable terminal echo
            os.write(fd, answer.encode()+b'\n')
            output = output.replace(prompt.encode(), b'', 1)
        while True:
            try:
                if select.select([fd], [], [], .1)[0]: output += os.read(fd, 65536)
            except OSError:
                break
            assert time.monotonic() < deadline, (name, output)
        _, status = os.waitpid(pid, 0)
        os.close(fd)
        text = output.decode()
        assert 'abc123' not in text, text
        if fail or name == 'empty': assert os.waitstatus_to_exitcode(status) == 1, text
        else: assert os.waitstatus_to_exitcode(status) == 0, text
        if name == 'compile-decline':
            assert not (root/'secrets/token').exists()
        elif name == 'decline':
            assert (root/'secrets/token').read_text() == 'old'
            assert '2020-01-01' in text
        elif not fail and name not in ('empty', 'compile-decline'):
            for path in ['secrets', 'data']: assert (root/path).stat().st_mode & 0o777 == 0o700
            for path in (root/'secrets').iterdir(): assert path.stat().st_mode & 0o777 == 0o600
        if verify == '0': assert ('Token válido. Hola, Ana' if 'fullname' in curl else 'Aviso:') in text
        if fail: assert fail in text
        print('PASS', name)

p='intento 1/3): '
run('success', [(p, '  "MoodleSessionag=abc123"  ')])
run('decline', [('¿Quieres reemplazarlo? [s/N]', '')], existing=True)
run('replace', [('¿Quieres reemplazarlo? [s/N]', 's'), (p, "'abc123'")], existing=True)
run('retry', [(p,''), ('intento 2/3): ','abc123')])
run('empty', [(p,''), ('intento 2/3): ',''), ('intento 3/3): ','')])
run('expired', [(p,'abc123')], fail='session expired')
run('collision', [(p,'abc123')], fail='must not already exist')
run('verify-valid', [(p,'abc123')], verify='0', curl='{"fullname":"Ana"}')
run('verify-invalid', [(p,'abc123')], verify='0', curl='{"exception":"invalidtoken"}')

run('compile', [('¿Quieres compilarlo con Docker? [s/N]', 's'), (p,'abc123')], missing=True)
run('compile-decline', [('¿Quieres compilarlo con Docker? [s/N]', '')], missing=True)
