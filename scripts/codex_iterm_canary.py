#!/usr/bin/env python3
"""Opt-in iTerm2 placement acceptance with mock Codex children and scratch state.

Opens two test windows, then tests pane/tab/window placement and stale anchors.
Closes only windows this invocation created. Does not invoke a model or use user
Codex configuration. Requires explicit GUI automation authorization.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time


def osa(script):
    result = subprocess.run(['osascript', '-e', script], text=True, capture_output=True, timeout=15)
    if result.returncode:
        raise RuntimeError(result.stderr.strip())
    return result.stdout.strip()


def windows():
    script = '''tell application "iTerm2"
set resultRows to ""
repeat with w in windows
set tabNumber to 0
repeat with t in tabs of w
set tabNumber to tabNumber + 1
repeat with s in sessions of t
set resultRows to resultRows & (id of w as text) & "|" & (tabNumber as text) & "|" & (id of s as text) & linefeed
end repeat
end repeat
end repeat
return resultRows
end tell'''
    return [line.split('|') for line in osa(script).splitlines() if line]


def run(args):
    cbus = str(Path(args.cbus).resolve())
    root = Path(tempfile.mkdtemp(prefix='cbus-iterm-check-', dir='/tmp')).resolve()
    report = {'root': str(root), 'cbusSha256': hashlib.sha256(Path(cbus).read_bytes()).hexdigest(),
              'proof': 'actual iTerm2 placement; mock children; no model calls', 'checks': {}, 'passed': False}
    print(f'iTerm test artifacts: {root}', flush=True)
    created, pids = set(), []
    def check(name, condition):
        report['checks'][name] = bool(condition)
        if not condition:
            raise AssertionError(name)
    try:
        for name in ('bin', 'home', 'codex', 'work', 'reports'):
            (root / name).mkdir()
        fake = root / 'bin/codex'
        fake.write_text('#!' + sys.executable + '\n' +
                        'import json,os,pathlib,sys,time\n' +
                        'root=pathlib.Path(os.environ["CBUS_DIR"]).parent\n' +
                        'keys=("HOME","CODEX_HOME","CBUS_DIR","ITERM_SESSION_ID","CODEX_THREAD_ID","CBUS_SESSION_ID")\n' +
                        'data={"pid":os.getpid(),"argv":sys.argv,"cwd":os.getcwd(),"env":{k:os.environ[k] for k in keys if k in os.environ}}\n' +
                        '(root/"reports"/(str(os.getpid())+".json")).write_text(json.dumps(data))\n' +
                        'time.sleep(120)\n')
        fake.chmod(0o700)
        anchor_script = root / 'anchor.py'
        anchor_script.write_text('import os,pathlib,time\n' +
                                 'pathlib.Path(' + repr(str(root)) + ',str(os.getpid())+".pid").write_text(str(os.getpid()))\n' +
                                 'time.sleep(120)\n')
        def new_window():
            command = sys.executable + ' ' + str(anchor_script)
            result = osa('tell application "iTerm2"\nset w to (create window with default profile command ' + json.dumps(command) + ')\nreturn (id of w as text) & "|" & (id of current session of w as text)\nend tell')
            wid, sid = result.split('|')
            created.add(wid)
            return wid, sid
        anchor_window, anchor = new_window()
        distractor_window, _ = new_window()
        env = os.environ.copy()
        for key in ('TMUX', 'TMUX_PANE', 'CLAUDE_CONFIG_DIR'):
            env.pop(key, None)
        env.update(HOME=str(root/'home'), CODEX_HOME=str(root/'codex'), CBUS_DIR=str(root/'bus'),
                   PATH=str(root/'bin')+os.pathsep+env.get('PATH',''), ITERM_SESSION_ID='w0t0p0:'+anchor,
                   CODEX_THREAD_ID='parent-must-not-leak', CBUS_SESSION_ID='parent-must-not-leak')
        anchor_tab = next(row[1] for row in windows() if row[2] == anchor)
        for target in ('pane', 'tab', 'window'):
            before = set((root/'reports').glob('*.json'))
            old_windows = {row[0] for row in windows()}
            result = subprocess.run([cbus, 'spawn', target, 'isolated-iterm', '--harness', 'codex', '--name', target,
                                     '--profile', 'isolated'], cwd=root/'work', env=env, text=True, capture_output=True, timeout=20)
            check(target+'_spawn_succeeds', result.returncode == 0)
            deadline = time.monotonic()+15
            while not (set((root/'reports').glob('*.json'))-before) and time.monotonic()<deadline:
                time.sleep(.1)
            files = set((root/'reports').glob('*.json'))-before
            check(target+'_child_started', len(files)==1)
            child = json.loads(next(iter(files)).read_text())
            pids.append(child['pid'])
            sid = child['env']['ITERM_SESSION_ID'].split(':',1)[-1]
            rows = windows()
            row = next(row for row in rows if row[2] == sid)
            if target == 'pane':
                check('pane_uses_anchor_tab', row[0] == anchor_window and row[1] == anchor_tab)
            elif target == 'tab':
                check('tab_uses_anchor_window', row[0] == anchor_window and row[1] != anchor_tab)
            else:
                created.add(row[0])
                check('window_is_new', row[0] not in old_windows)
            check(target+'_cwd_and_homes', child['cwd']==str(root/'work') and child['env'].get('HOME')==str(root/'home') and child['env'].get('CODEX_HOME')==str(root/'codex'))
            check(target+'_identity_cleared', not child['env'].get('CODEX_THREAD_ID') and not child['env'].get('CBUS_SESSION_ID'))
            check(target+'_profile_preserved', child['argv'][1:3]==['--profile','isolated'])
        env['ITERM_SESSION_ID'] = 'w0t0p0:00000000-0000-0000-0000-000000000000'
        result = subprocess.run([cbus,'spawn','tab','isolated-iterm','--harness','codex','--name','stale'], cwd=root/'work', env=env, capture_output=True, text=True, timeout=20)
        check('stale_anchor_refused', result.returncode != 0 and 'not found' in result.stderr)
        check('stale_reservation_removed', not (root/'bus/isolated-iterm/stale').exists())
        report['passed'] = True
    except Exception as error:
        report['error'] = str(error)
    finally:
        pids.extend(int(path.read_text()) for path in root.glob('*.pid'))
        for pid in pids:
            try: os.kill(pid, signal.SIGTERM)
            except ProcessLookupError: pass
        cleanup_errors = []
        for wid in created:
            try:
                osa('tell application "iTerm2"\nrepeat with w in windows\nif (id of w as text) is '+json.dumps(wid)+' then close w\nend repeat\nend tell')
            except Exception as error:
                cleanup_errors.append(str(error))
        report['cleanupErrors'] = cleanup_errors
        report['passed'] = report['passed'] and not cleanup_errors
        (root/'result.json').write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(report,indent=2))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cbus',required=True)
    raise SystemExit(run(parser.parse_args()))
