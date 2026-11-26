#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
python3 - <<'PY'
import os,secrets,pathlib,shlex
p=pathlib.Path('.env')
data_dir=os.path.abspath(os.environ.get('WEBUDDY_DATA_DIR','.data'))
if p.exists():
 text=p.read_text()
 if 'WEBUDDY_DATA_DIR=' not in text:
  with p.open('a') as f: f.write(f'\nWEBUDDY_DATA_DIR={shlex.quote(data_dir)}\n')
  print('Added WEBUDDY_DATA_DIR to existing .env')
 else:
  print('Keeping existing .env')
else:
 password=secrets.token_hex(24)
 http_port=os.environ.get('AGENTFLOW_HTTP_PORT','8090');mysql_port=os.environ.get('WEBUDDY_MYSQL_PORT','33315')
 data={'WEBUDDY_DB_PASSWORD':password,'WEBUDDY_DB_ROOT_PASSWORD':secrets.token_hex(24),'WEBUDDY_MYSQL_PORT':mysql_port,'COMPOSE_PROJECT_NAME':os.environ.get('COMPOSE_PROJECT_NAME','webuddy'),'AGENTFLOW_HTTP_PORT':http_port,'AGENTFLOW_MYSQL_DSN':f'webuddy:{password}@tcp(127.0.0.1:{mysql_port})/webuddy','AGENTFLOW_SESSION_SECRET':secrets.token_hex(32),'AGENTFLOW_AUTH_REQUIRED':'true','WEBUDDY_DATA_DIR':data_dir,'WEBUDDY_BIND':os.environ.get('WEBUDDY_BIND','0.0.0.0'),'WEBUDDY_PUBLIC_URL':os.environ.get('WEBUDDY_PUBLIC_URL',''),'WEBUDDY_IMAGE':os.environ.get('WEBUDDY_IMAGE',''),'AGENTFLOW_GITHUB_CLIENT_ID':os.environ.get('AGENTFLOW_GITHUB_CLIENT_ID','')}
 fd=os.open(str(p),os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
 with os.fdopen(fd,'w') as f: f.write(''.join(f'{k}={shlex.quote(v)}\n' for k,v in data.items()))
 print('Created .env with unique local secrets')
PY
