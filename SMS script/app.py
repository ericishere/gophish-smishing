#!/usr/bin/env python3
import os
import sys
import csv
import io
import subprocess
from flask import Flask, render_template, request, jsonify

# Import the core ADB functions from your existing send_sms.py
from send_sms import send_via_service, send_via_intent, get_android_version, check_device

app = Flask(__name__, template_folder='.')

# Whitelist of allowed send methods and SIM sub-ids to prevent parameter injection
ALLOWED_METHODS = {'service', 'intent'}


def validate_method(method):
    return method if method in ALLOWED_METHODS else None


def validate_sub_id(sub_id):
    try:
        n = int(sub_id)
        return n if n >= 0 else None
    except (TypeError, ValueError):
        return None


@app.route('/')
def index():
    return render_template('index.html')


@app.route('/parse', methods=['POST'])
def parse_only():
    if 'csv_file' not in request.files:
        return jsonify({'success': False, 'message': 'No CSV file uploaded.'})

    file = request.files['csv_file']
    template = request.form.get('template', '').strip()

    if file.filename == '':
        return jsonify({'success': False, 'message': 'Empty file selected.'})
    if not template or '{url}' not in template:
        return jsonify({'success': False, 'message': 'Invalid template. Must contain {url} placeholder.'})

    try:
        raw_bytes = file.stream.read()
        encodings_to_try = ['utf-8-sig', 'utf-8', 'cp950', 'gbk', 'big5']
        decoded_text = None

        for enc in encodings_to_try:
            try:
                decoded_text = raw_bytes.decode(enc)
                break
            except UnicodeDecodeError:
                continue

        if decoded_text is None:
            return jsonify({'success': False, 'message': 'Failed to decode CSV.'})

        stream = io.StringIO(decoded_text, newline=None)
        reader = csv.DictReader(stream)

        headers = reader.fieldnames
        if 'Position' not in headers or 'Unique URL' not in headers:
            return jsonify({'success': False, 'message': 'CSV Headers mismatch! Format error.'})

        parsed_rows = []
        for row in reader:
            recipient = row.get('Position', '').strip()
            unique_url = row.get('Unique URL', '').strip()
            if not recipient or not unique_url:
                continue

            parsed_rows.append({
                'recipient': recipient,
                'url': unique_url,
                'full_content': template.replace('{url}', unique_url)
            })

        return jsonify({'success': True, 'rows': parsed_rows})
    except Exception as e:
        return jsonify({'success': False, 'message': str(e)})


# 全新功能：調用真實的 send_sms.py 進行 --dry-run
# 全新功能：調用真實的 send_sms.py 進行 --dry-run
@app.route('/real_dry_run', methods=['POST'])
def real_dry_run():
    data = request.get_json() or {}
    rows = data.get('rows', [])
    method = validate_method(data.get('method', 'intent'))  # Defaulting fallback to intent
    if method is None:
        return jsonify({'success': False, 'output': 'Invalid method. Must be "service" or "intent".'}), 400
    sub_id = validate_sub_id(data.get('sub_id', '1'))
    if sub_id is None:
        return jsonify({'success': False, 'output': 'Invalid sub_id. Must be a non-negative integer.'}), 400

    if not rows:
        return jsonify({'success': False, 'output': 'No data available to perform CLI dry-run.'})

    temp_csv = "temp_web_dry_run.csv"
    try:
        # 1. 建立符合 send_sms.py 標準欄位 (Recipient, Content) 的臨時變數檔案
        with open(temp_csv, mode='w', newline='', encoding='utf-8') as f:
            writer = csv.writer(f)
            writer.writerow(['Recipient', 'Content'])
            for row in rows:
                writer.writerow([row['recipient'], row['full_content']])

        # 2. 組裝符合特定格式的 CLI 指令 (把 flag 放在前面，檔案路徑置於末端)
        cmd = [sys.executable, "send_sms.py", "--delay", "3", "--method", method, "--dry-run"]
        if method == "service":
            cmd.extend(["--sub-id", str(sub_id)])
        cmd.append(temp_csv) # 置於最後

        # 確保子程序環境變數採用 UTF-8 避免字元崩潰
        sub_env = os.environ.copy()
        sub_env["PYTHONIOENCODING"] = "utf-8"

        # 3. 執行子行程並捕獲原始輸出流
        result = subprocess.run(cmd, capture_output=True, text=False, env=sub_env)

        stdout_str = result.stdout.decode('utf-8', errors='replace')
        stderr_str = result.stderr.decode('utf-8', errors='replace')

        combined_output = stdout_str + "\n" + stderr_str

        # 4. 清理臨時檔案
        if os.path.exists(temp_csv):
            os.remove(temp_csv)

        return jsonify({'success': True, 'output': combined_output.strip()})

    except Exception as e:
        if os.path.exists(temp_csv):
            os.remove(temp_csv)
        return jsonify({'success': False, 'output': f'Execution internal error: {str(e)}'})

@app.route('/execute', methods=['POST'])
def execute_real_send():
    data = request.get_json() or {}
    rows = data.get('rows', [])
    method = validate_method(data.get('method', 'service'))
    if method is None:
        return jsonify({'success': False, 'message': 'Invalid method. Must be "service" or "intent".'}), 400
    sub_id = validate_sub_id(data.get('sub_id', 1))
    if sub_id is None:
        return jsonify({'success': False, 'message': 'Invalid sub_id. Must be a non-negative integer.'}), 400

    if not rows:
        return jsonify({'success': False, 'message': 'No data rows available for execution.'})
    if not check_device():
        return jsonify({'success': False, 'message': 'ADB Error: No Android device detected via USB!'})

    android_version = get_android_version() or 13
    results = []
    success_count = 0
    failed_count = 0

    for row in rows:
        recipient = row.get('recipient', '').strip()
        message_content = row.get('full_content', '').strip()
        if not recipient or not message_content:
            continue

        if method == 'service':
            ok, stdout, stderr = send_via_service(recipient, message_content, android_version, sub_id)
        elif method == 'intent':
            ok, stdout, stderr = send_via_intent(recipient, message_content)
        else:
            continue

        if ok:
            success_count += 1
            status = 'SUCCESS'
        else:
            failed_count += 1
            status = f'FAILED ({stderr or "Unknown"})'

        results.append({'recipient': recipient, 'status': status})

    return jsonify({
        'success': True,
        'message': f'Execution complete. Sent: {success_count}, Failed: {failed_count}.',
        'detail': results
    })


if __name__ == '__main__':
    app.run(host=os.environ.get('SMS_WEB_HOST', '127.0.0.1'),
            port=int(os.environ.get('SMS_WEB_PORT', '5000')),
            debug=False,
            use_debugger=False,
            use_reloader=False)
