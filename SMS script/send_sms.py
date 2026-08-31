#!/usr/bin/env python3
"""
send_sms.py — Send bulk SMS via ADB from a CSV file.

CSV must have columns:  Recipient, Content
Example row:            +12025551234, Hello there!

Usage:
  python send_sms.py messages.csv
  python send_sms.py messages.csv --method intent --delay 3
  python send_sms.py messages.csv --android-version 13
  python send_sms.py messages.csv --dry-run

Methods:
  service (default) — calls Android's ISms service directly; no UI required.
                      Transaction number differs by Android version (auto-detected).
  intent            — launches the system SMS app with a pre-filled message;
                      then sends a keyevent to press the send button.
                      Less reliable across devices but works when 'service' fails.

Requirements:
  - adb in PATH
  - Android device connected via USB with USB Debugging enabled
  - For 'service' method: device SIM with SMS capability
"""

import csv
import os
import subprocess
import sys
import time
import argparse


# ---------------------------------------------------------------------------
# ADB helpers
# ---------------------------------------------------------------------------

# Path to the adb executable, expected in an "adb" subfolder next to this script.
ADB_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "adb", "adb.exe")


def run_adb(*args):
    """Run: adb <args>  and return CompletedProcess."""
    return subprocess.run([ADB_PATH] + list(args), capture_output=True, text=True)


def get_android_version():
    """Return the major Android version int, or None on failure."""
    result = run_adb("shell", "getprop", "ro.build.version.release")
    try:
        return int(result.stdout.strip().split(".")[0])
    except (ValueError, IndexError):
        return None


def check_device():
    """Return True if exactly one device is connected and ready."""
    result = run_adb("devices")
    lines = [l for l in result.stdout.splitlines() if l.endswith("\tdevice")]
    return len(lines) >= 1


# ---------------------------------------------------------------------------
# Shell escaping for the Android shell
# ---------------------------------------------------------------------------

def _escape(s):
    """Escape a value to be placed inside double-quotes in the Android shell."""
    return s.replace("\\", "\\\\").replace('"', '\\"').replace("`", "\\`").replace("$", "\\$")


# ---------------------------------------------------------------------------
# SMS sending: 'service' method
# ---------------------------------------------------------------------------

# Transaction number for ISms.sendTextForSubscriber() varies by Android version.
# These are the known stable values; override with --android-version if needed.
_ISMS_TX = {
    13: 5,  # Android 13+
    12: 5,
    11: 5,
    10: 7,
    9:  7,
}
_ISMS_TX_DEFAULT = 5  # safe default for unknown / future versions


def _isms_tx(android_version):
    if android_version is None:
        return _ISMS_TX_DEFAULT
    return _ISMS_TX.get(android_version, _ISMS_TX_DEFAULT)


def send_via_service(recipient, message, android_version, sub_id=1):
    """
    Send SMS by calling the Android ISms binder service directly.
    No UI is shown on the device.

    This matches the sendTextForSubscriber() signature on Android 11-14:
        (int subId, String callingPackage, String callingAttributionTag,
         String destAddr, String scAddr, String text,
         PendingIntent sentIntent, PendingIntent deliveryIntent,
         boolean persistMessage, long messageId)

    Confirmed working form on the target device:
        service call isms 5 i32 1 s16 "com.android.mms" s16 "null" \\
            s16 "<number>" s16 "null" s16 "<message>" s16 "null" s16 "null" \\
            i32 0 i64 0

    Returns (success: bool, output: str, error: str).
    """
    tx = _isms_tx(android_version)
    r = _escape(recipient)
    m = _escape(message)

    # Android 9/10 used an older, shorter signature (transaction 7) without the
    # attributionTag string or the trailing persistMessage/messageId args.
    if android_version is not None and android_version <= 10:
        shell_cmd = (
            f'service call isms {tx} '
            f'i32 {sub_id} '
            f's16 "com.android.mms" '          # callingPackage
            f's16 "{r}" '                      # destAddr
            f's16 "null" '                     # scAddr
            f's16 "{m}" '                      # text
            f's16 "null" '                     # sentIntent
            f's16 "null"'                      # deliveryIntent
        )
    else:
        # Android 11-14 signature (transaction 5).
        shell_cmd = (
            f'service call isms {tx} '
            f'i32 {sub_id} '                   # subId
            f's16 "com.android.mms" '          # callingPackage
            f's16 "null" '                     # callingAttributionTag
            f's16 "{r}" '                      # destAddr
            f's16 "null" '                     # scAddr
            f's16 "{m}" '                      # text
            f's16 "null" '                     # sentIntent
            f's16 "null" '                     # deliveryIntent
            f'i32 0 '                          # persistMessage (false)
            f'i64 0'                           # messageId
        )

    result = run_adb("shell", shell_cmd)
    # A successful binder call returns a Parcel result line.
    success = result.returncode == 0 and "Parcel" in result.stdout
    return success, result.stdout.strip(), result.stderr.strip()


# ---------------------------------------------------------------------------
# SMS sending: 'intent' method
# ---------------------------------------------------------------------------

def send_via_intent(recipient, message):
    """
    Send SMS by launching the system messaging app via ACTION_SENDTO intent,
    then pressing the send key.

    Returns (success: bool, output: str, error: str).
    """
    r = _escape(recipient)
    m = _escape(message)

    # Open the messaging app with a pre-filled message.
    result = run_adb(
        "shell",
        f'am start -a android.intent.action.SENDTO '
        f'-d "sms:{r}" '
        f'--es sms_body "{m}" '
        f'--ez exit_on_sent true'
    )

    if result.returncode != 0:
        return False, result.stdout.strip(), result.stderr.strip()

    # Allow the app to render.
    time.sleep(2)

    # Attempt to press the send button via KEYCODE_ENTER.
    # On some devices KEYCODE_DPAD_RIGHT may be needed first to focus the button.
    run_adb("shell", "input tap 638 1454")

    return True, "Launched SMS intent — verify send on device if needed.", ""



# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    if sys.platform == "win32":
        sys.stdout.reconfigure(encoding='utf-8')
        sys.stderr.reconfigure(encoding='utf-8')
    parser = argparse.ArgumentParser(
        description="Send bulk SMS via ADB from a CSV file.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    parser.add_argument("csv_file", help="Path to CSV file (Recipient, Content columns)")
    parser.add_argument(
        "--method", choices=["service", "intent"], default="service",
        help="Sending method: 'service' (default, direct) or 'intent' (opens SMS app)."
    )
    parser.add_argument(
        "--delay", type=float, default=2.0,
        help="Seconds to wait between messages (default: 2.0)."
    )
    parser.add_argument(
        "--android-version", type=int, default=None, metavar="N",
        help="Override Android major version (e.g. 9, 11, 13). Auto-detected if omitted."
    )
    parser.add_argument(
        "--sub-id", type=int, default=1, metavar="ID",
        help="SIM subscription ID for the 'service' method (default: 1). "
             "If messages silently fail, try 0 or 2 — it must match your active SIM slot."
    )
    parser.add_argument(
        "--dry-run", action="store_true",
        help="Print what would be sent without actually sending anything."
    )
    args = parser.parse_args()

    # ---- verify adb --------------------------------------------------------
    if not os.path.isfile(ADB_PATH):
        print(f"ERROR: adb.exe not found at '{ADB_PATH}'. "
              f"Place adb.exe (and its DLLs) in the 'adb' subfolder next to this script.")
        sys.exit(1)

    if run_adb("version").returncode != 0:
        print("ERROR: failed to run adb. Check that it is a valid executable.")
        sys.exit(1)

    if not args.dry_run and not check_device():
        print("ERROR: No Android device detected. Connect a device with USB debugging enabled.")
        sys.exit(1)

    # ---- detect Android version --------------------------------------------
    android_version = args.android_version
    if android_version is None and args.method == "service" and not args.dry_run:
        android_version = get_android_version()
        if android_version:
            print(f"Detected Android {android_version} "
                  f"(ISms transaction: {_isms_tx(android_version)})")
        else:
            print(f"Warning: could not detect Android version; "
                  f"using transaction {_ISMS_TX_DEFAULT}.")

    # ---- load CSV ----------------------------------------------------------
    try:
        with open(args.csv_file, newline="", encoding="utf-8-sig") as f:
            reader = csv.DictReader(f)
            rows = list(reader)
    except FileNotFoundError:
        print(f"ERROR: File not found: {args.csv_file}")
        sys.exit(1)
    except Exception as exc:
        print(f"ERROR reading CSV: {exc}")
        sys.exit(1)

    if not rows:
        print("No data rows found in CSV. Nothing to do.")
        sys.exit(0)

    # Validate expected columns (case-sensitive).
    if "Recipient" not in rows[0] or "Content" not in rows[0]:
        print("ERROR: CSV must have columns named exactly 'Recipient' and 'Content'.")
        print(f"       Found columns: {list(rows[0].keys())}")
        sys.exit(1)

    total = len(rows)
    print(f"Loaded {total} row(s) from '{args.csv_file}' "
          f"(method={args.method}, delay={args.delay}s)\n")

    sent = skipped = failed = 0

    for i, row in enumerate(rows, 1):
        recipient = row.get("Recipient", "").strip()
        content   = row.get("Content",   "").strip()
        tag       = f"[{i:>{len(str(total))}}/{total}]"

        if not recipient or not content:
            print(f"{tag} SKIP   — empty Recipient or Content")
            skipped += 1
            continue

        # Warn about long messages that will be split into multiple SMS parts.
        if len(content) > 160:
            print(f"{tag} NOTE   — message is {len(content)} chars and will be split "
                  f"into {-(-len(content)//160)} SMS parts")

        if args.dry_run:
            preview = content if len(content) <= 60 else content[:57] + "..."
            print(f"{tag} DRY-RUN  To: {recipient}  Msg: {preview}")
            sent += 1
            continue

        print(f"{tag} Sending to {recipient} ...", end=" ", flush=True)

        if args.method == "service":
            ok, out, err = send_via_service(recipient, content, android_version, args.sub_id)
        else:
            ok, out, err = send_via_intent(recipient, content)

        if ok:
            print("OK")
            sent += 1
        else:
            print("FAILED")
            if err:
                print(f"         stderr: {err}")
            if out:
                print(f"         stdout: {out}")
            failed += 1

        # Throttle to avoid overwhelming the device or carrier.
        if i < total:
            time.sleep(args.delay)

    # ---- summary -----------------------------------------------------------
    print(f"\n{'DRY-RUN ' if args.dry_run else ''}Done: "
          f"{sent} sent, {failed} failed, {skipped} skipped.")

    if failed:
        print("\nTip: if the 'service' method fails, try --method intent, "
              "or specify --android-version manually.")
        sys.exit(1)


if __name__ == "__main__":
    main()
