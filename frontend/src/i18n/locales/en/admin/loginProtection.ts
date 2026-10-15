export default {
  loginProtection: {
    title: 'Login Protection',
    description: 'Count failed panel logins and ban the source IP from all of Sub2API.',
    rules: 'Protection rules', enabled: 'Enable login protection',
    window_minutes: 'Window (minutes)', max_failures: 'Allowed failures', ban_hours: 'Ban duration (hours)',
    range: 'Integer, 1–{max}', invalidRange: 'Enter an integer between 1 and {max}.',
    validationFailed: 'Check the following settings:',
    summary: 'More than {failures} failures within {minutes} minutes bans the IP for {hours} hours, starting with failure {trigger}.',
    scopeNote: 'Password and login two-factor failures share one counter. Successful logins do not reset it. Everyone sharing a public IP shares its ban. Bans block panel, model API and new WebSocket requests; existing connections finish normally.',
    disableNote: 'Disabling pauses counting and enforcement while records expire normally. Re-enabling restores unexpired bans. Rule changes do not change existing expiry times.',
    bans: 'Currently banned IPs', blockedAt: 'Banned at', expiresAt: 'Expires at',
    empty: 'There are no active bans.', reload: 'Refresh', unban: 'Unban', unbanIP: 'Unban IP {ip}',
    unbanNote: 'Unbanning also clears the failure counter. Banned IPs cannot access this page; manage them from another, unbanned IP.',
    saved: 'Login protection settings saved', unbanned: 'Unbanned {ip}',
    loadFailed: 'Could not load login protection. Please retry.', saveFailed: 'Could not save login protection.', unbanFailed: 'Could not unban this IP. Please retry.',
  },
}
