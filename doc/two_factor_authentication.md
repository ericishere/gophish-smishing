# Two-factor authentication

Gophish supports multiple named authenticator apps using TOTP. Google
Authenticator and compatible apps can scan the setup QR code or use the manual
setup key. Gophish generates QR codes locally.

## Administrator setup and recovery

In **User Management**, select **2FA required** when creating or editing a user.
After the user's password is accepted, Gophish requires enrollment before issuing
a signed-in session. If a password change is also required, that step follows
2FA setup. The administrator created during initial Gophish installation is not
required to enroll; existing users also remain optional after upgrading.

The users table shows whether 2FA is enabled or setup is pending. **Reset 2FA**
(the unlock icon) removes a user's authenticators and pending enrollment, signs
out their browser sessions, and preserves the **2FA required** setting. The
administrator must confirm with their own password. A required user must enroll
again at their next sign-in. Verify the user's identity before using recovery.

Disabling **2FA required** allows deletion of the last method; it does not remove
existing methods or turn off code verification at sign-in. Administrators
impersonating a user must also pass that user's verification or required setup.

## User settings

Open **Settings → Two-Factor Authentication** to add an authenticator. Confirm
your current password, scan the QR code, give the method a name, and enter its
six-digit code. A method is only activated after successful confirmation. Up to
10 methods can be added, so a second device can be kept as a backup.

Deleting a method requires the current password. If 2FA is required, add a
replacement before deleting your last method. If you cannot access any enrolled
method, ask an administrator to reset 2FA.

Whenever any method exists, sign-in requires a code from one of those methods,
regardless of the administrator's requirement setting. Codes already used for
enrollment or sign-in cannot be reused; wait for a fresh code in the app.

## Operational behavior

- TOTP uses SHA-1, six digits, a 30-second period, and a one-step clock tolerance.
  Keep server and authenticator device clocks synchronized.
- Pending verification/enrollment expires after 10 minutes. Starting a new flow
  for an account replaces its previous pending flow.
- Five incorrect codes block verification for that account for five minutes.
  Starting a new browser session or password login does not clear the limit.
- Adding/deleting/resetting methods, changing the requirement, changing the
  password, or locking the account invalidates previous browser sessions. A user
  changing their own methods or password keeps their current session.
- API tokens remain noninteractive bearer credentials. Locked accounts and
  required accounts without an enrolled method cannot access the API. TOTP
  secrets and replay counters are excluded from API responses.
- SQLite and MySQL schema migrations run at startup. Back up the database before
  upgrading. TOTP secrets must be recoverable for verification and are stored in
  the database, like other Gophish service credentials; protect database files,
  connections, and backups. Serve the admin interface over HTTPS.
- Rebuild frontend assets with `npm ci`, `npx gulp build`, and `npx webpack` when
  deploying from source, as with the existing Gophish frontend.

The `two_factor_required` field is accepted by the user creation/update API.
Only administrators can change the requirement; omitted fields preserve the
current policy. Authenticator enrollment/deletion and administrator resets use
session-authenticated, CSRF-protected forms rather than API-key-only endpoints.

The implementation follows [RFC 6238](https://www.rfc-editor.org/rfc/rfc6238) and
Google Authenticator's [key URI format](https://github.com/google/google-authenticator/wiki/Key-Uri-Format).
