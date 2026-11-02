#!/usr/bin/env python3
"""Opt-in real Codex/CHV verification; no credential, resource or VM is guessed."""
import base64
import json
import os
import pathlib
import re
import stat
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


def protected_json(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd) as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_size > 65536:
            raise RuntimeError('凭证文件必须是权限 0600 的普通小文件')
        return json.load(source)


def main():
    report = {'environment': os.getenv('ISODO_MCP_E2E_API', ''), 'steps': [], 'status': 'BLOCKED', 'providers': {'notion':'BLOCKED'}, 'sandbox_id': None, 'cleanup':'无需清理，未创建沙箱'}
    required = ['ISODO_MCP_E2E_API', 'ISODO_MCP_E2E_HEADERS_FILE',
                'ISODO_MCP_E2E_MODEL_RULES_FILE', 'ISODO_MCP_E2E_MODEL_BASE_URL',
                'ISODO_MCP_E2E_MODEL', 'ISODO_MCP_E2E_NOTION_PAGE']
    missing = [name for name in required if not os.getenv(name)]
    if missing or os.getenv('ISODO_MCP_E2E_TEST_RESOURCES_APPROVED') != '1':
        report['reason'] = '缺少明确授权的测试资源/模型连接或会话配置：' + ', '.join(missing)
        print(json.dumps(report, ensure_ascii=False, indent=2))
        pathlib.Path(os.getenv('ISODO_MCP_E2E_REPORT', '/tmp/isodo-official-mcp-e2e.json')).write_text(json.dumps(report,ensure_ascii=False,indent=2))
        return 2
    origin = os.environ['ISODO_MCP_E2E_API'].rstrip('/')
    parsed = urllib.parse.urlsplit(origin)
    if parsed.scheme != 'https' or parsed.username or parsed.query or parsed.fragment or parsed.path:
        raise RuntimeError('验收 API 必须是可信 HTTPS origin')
    headers = protected_json(os.environ['ISODO_MCP_E2E_HEADERS_FILE'])
    rules = protected_json(os.environ['ISODO_MCP_E2E_MODEL_RULES_FILE'])
    page = os.environ['ISODO_MCP_E2E_NOTION_PAGE']
    page_url = urllib.parse.urlsplit(page)
    if page_url.scheme != 'https' or not (
        page_url.hostname == 'notion.so' or (page_url.hostname or '').endswith('.notion.so') or
        (page_url.hostname or '').endswith('.notion.site')):
        raise RuntimeError('仅接受明确指定的 Notion 测试页面')
    base_url = os.environ['ISODO_MCP_E2E_MODEL_BASE_URL'].rstrip('/')
    model_url = urllib.parse.urlsplit(base_url)
    if model_url.scheme != 'https' or model_url.username or model_url.query or model_url.fragment:
        raise RuntimeError('模型地址必须是可信 HTTPS，不能内嵌凭证')
    if len(rules.get('rules', [])) != 1:
        raise RuntimeError('测试模型规则文件必须只含一个授权的模型规则')
    rule = rules['rules'][0]
    if rule.get('spec', {}).get('base_url', '').rstrip('/') != base_url or rule.get('spec', {}).get('if_headers', {}).get('Authorization') != 'Bearer isodo-e2e-placeholder':
        raise RuntimeError('模型规则必须精确匹配模型地址和测试占位 Authorization')
    sandbox = None
    # Explicit no-redirect transport: never forward account credentials elsewhere.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args):
            return None
    client = urllib.request.build_opener(NoRedirect)

    def call(method, path, data=None, expected=(200,), content_type='application/json'):
        payload = data if isinstance(data, bytes) else None if data is None else json.dumps(data).encode()
        req = urllib.request.Request(origin + path, payload, method=method, headers=headers)
        if payload is not None:
            req.add_header('Content-Type', content_type)
        if path.endswith('/commands/run'):
            req.add_header('Idempotency-Key', str(uuid.uuid4()))
        try:
            response = client.open(req, timeout=180)
        except urllib.error.HTTPError as error:
            if error.code not in expected:
                raise RuntimeError(f'HTTP {error.code}：{method} {path}') from None
            response = error
        with response:
            if response.code not in expected:
                raise RuntimeError(f'非预期 HTTP {response.code}')
            raw = response.read(8 << 20)
            if response.code == 404 and 404 in expected:
                return None
        return json.loads(raw) if raw else None

    def step(name, action, tool=None):
        started = time.monotonic()
        record = {'name': name, 'sandbox_id': sandbox, 'tool': tool}
        try:
            result = action()
            record['status'] = 'PASS'
            return result
        except Exception as error:
            record.update(status='FAIL', reason=str(error))
            raise
        finally:
            record['seconds'] = round(time.monotonic() - started, 3)
            report['steps'].append(record)
            print(json.dumps(record, ensure_ascii=False), flush=True)

    def command(argv, timeout=120):
        result = call('POST', f'/v1/sandboxes/{sandbox}/commands/run',
                      {'argv': argv, 'cwd': '/workspace', 'timeout_seconds': timeout,
                       'env': {'CODEX_HOME': '/workspace/.codex'}})
        if result.get('exit_code') != 0 or result.get('output_incomplete'):
            raise RuntimeError('Guest 命令失败或输出不完整（不打印可能含私人内容的输出）')
        return result['stdout']

    def upload(path, data):
        boundary = 'isodo-' + uuid.uuid4().hex
        body = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="config.toml"\r\nContent-Type: application/octet-stream\r\n\r\n'.encode() + data.encode() + f'\r\n--{boundary}--\r\n'.encode())
        return call('POST', f'/v1/sandboxes/{sandbox}/files?path=' + urllib.parse.quote(path), body,
                    content_type='multipart/form-data; boundary=' + boundary)

    try:
        for provider in ['notion']:
            connection = call('GET', f'/v1/mcp/connections/{provider}')
            if connection.get('status') != 'connected':
                report['reason'] = f'请先在市场完成 {provider} 的真人 OAuth 授权和测试项目选择'
                return 2
        item = call('POST', '/v1/sandboxes', {'name': 'official-mcp-e2e-' + uuid.uuid4().hex[:8]}, (202,))
        sandbox = item['id']
        report['sandbox_id'] = sandbox
        def running():
            deadline = time.monotonic() + 180
            while time.monotonic() < deadline:
                current = call('GET', f'/v1/sandboxes/{sandbox}')
                if current['observed_state'] == 'running':
                    return
                if current['observed_state'] == 'error':
                    raise RuntimeError('沙箱启动失败')
                time.sleep(1)
            raise RuntimeError('沙箱启动超时')
        step('创建专用 CHV 并等待 running，全程不打开工作台', running)
        step('仅将模型真实凭证发送 Control/Node 注入代理', lambda: call('PUT', f'/v1/sandboxes/{sandbox}/injections', rules))
        call('POST', f'/v1/sandboxes/{sandbox}/directories', {'path': '/workspace/.codex'})
        config = 'model_provider = "isodo_e2e"\nmodel = ' + json.dumps(os.environ['ISODO_MCP_E2E_MODEL']) + '\n[model_providers.isodo_e2e]\nname = "Isodo E2E"\nwire_api = "responses"\nbase_url = ' + json.dumps(base_url) + '\nexperimental_bearer_token = "isodo-e2e-placeholder"\n'
        step('写入仅含占位符的 Codex 配置', lambda: upload('/workspace/.codex/config.toml', config))
        for provider in ['notion']:
            step(f'显式启用 {provider}', lambda provider=provider: call('PUT', f'/v1/sandboxes/{sandbox}/mcp/{provider}', expected=(200, 202)))
        prompts = {
            'notion': ('notion-fetch', f'Use only the isodo_notion MCP server. Call notion-fetch once to read this explicitly authorized test page: {page}. Do not search other pages or modify anything. Give a short confirmation.')}
        for provider, (tool, prompt) in prompts.items():
            def invoke(provider=provider, tool=tool, prompt=prompt):
                output = command(['codex', 'exec', '--skip-git-repo-check', '--sandbox', 'danger-full-access', '--json', prompt])
                found = False
                for line in output.splitlines():
                    try:
                        event = json.loads(line)
                    except ValueError:
                        continue
                    item = event.get('item', {})
                    if item.get('type') == 'mcp_tool_call' and item.get('server') == 'isodo_' + provider and item.get('tool') == tool and item.get('status') == 'completed' and not item.get('error') and not (item.get('result') or {}).get('isError'):
                        found = True
                if not found:
                    raise RuntimeError('未确认成功的 Codex MCP 工具事件；不能以模型文字回复代替工具调用证据')
            step(f'Codex 实际只读调用 {provider}', invoke, tool)
            report['providers'][provider] = 'PASS'
        def reject(provider, tool, arguments):
            message = json.dumps({'jsonrpc': '2.0', 'id': 1, 'method': 'tools/call', 'params': {'name': tool, 'arguments': arguments}})
            encoded = base64.b64encode(message.encode()).decode()
            script = "import urllib.request,urllib.error,base64\nr=urllib.request.Request('http://127.0.0.1:31080/isodo-mcp/" + provider + "',base64.b64decode('" + encoded + "'),headers={'Authorization':'Bearer isodo-mcp-placeholder','Content-Type':'application/json'})\ntry:\n urllib.request.urlopen(r,timeout=30);raise SystemExit(1)\nexcept urllib.error.HTTPError as e:\n assert e.code==403;print('DENIED')\n"
            if command(['python3', '-c', script], 40).strip() != 'DENIED':
                raise RuntimeError('拒绝校验失败')
        step('Notion 写工具被 Broker 拒绝', lambda: reject('notion', 'notion-create-pages', {}), 'notion-create-pages')
        for provider, (tool, _) in prompts.items():
            step(f'解绑 {provider}', lambda provider=provider: call('DELETE', f'/v1/sandboxes/{sandbox}/mcp/{provider}', expected=(200, 202)))
            step(f'解绑后 {provider} 调用拒绝', lambda provider=provider, tool=tool: reject(provider, tool, {}), tool)
        report['status'] = 'PASS'
        result = 0
    except Exception as error:
        report.update(status='FAIL', reason=str(error))
        result = 1
    finally:
        if sandbox:
            try:
                call('DELETE', f'/v1/sandboxes/{sandbox}', expected=(202, 204))
                deadline = time.monotonic() + 90
                while time.monotonic() < deadline:
                    current = call('GET', f'/v1/sandboxes/{sandbox}', expected=(200, 404))
                    if not current or (current.get('deletion') or {}).get('completed_at'):
                        report['cleanup'] = 'PASS'
                        break
                    time.sleep(1)
                else:
                    report['cleanup'] = 'FAIL: 删除未确认完成'
            except Exception:
                report['cleanup'] = 'FAIL: 请按记录的 Sandbox ID 清理此专用测试资源'
        else:
            report['cleanup'] = '无需清理，未创建沙箱'
        cleanup_failed = bool(sandbox and report.get('cleanup') != 'PASS')
        if cleanup_failed:
            report['status'] = 'FAIL'
        print(json.dumps(report, ensure_ascii=False, indent=2))
        pathlib.Path(os.getenv('ISODO_MCP_E2E_REPORT', '/tmp/isodo-official-mcp-e2e.json')).write_text(json.dumps(report, ensure_ascii=False, indent=2))
        if cleanup_failed:
            result = 1
    return result


if __name__ == '__main__':
    sys.exit(main())
