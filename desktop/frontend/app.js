const root = document.querySelector('#app');
const params = new URLSearchParams(location.search);
const preview = params.get('preview') === '1';
let runtime;
if (!preview) {
  try { runtime = await import('/wails/runtime.js'); }
  catch { root.innerHTML = '<main class="empty-state"><h1>Open the iOxide desktop app</h1><p>The desktop connection is unavailable. Close this window and restart iOxide.</p></main>'; throw new Error('Desktop runtime unavailable'); }
}

const sample = { username: 'alex@example.com', locked: false, error: '', calendars: 3, loaded: 3, events: 248, listed: true, lastSync: new Date().toISOString(), lastClient: '0001-01-01T00:00:00Z' };
let state = { running: true, address: 'http://127.0.0.1:8081/caldav/', accounts: [], stage: 'account', error: '' };
let view = preview ? params.get('screen') || 'welcome' : 'welcome';
let selectedUser = '', client = 'apple', remote = false, connectionURL = '', network = null;
let busy = false, error = '', message = '', revealed = '', revealTimer;
let removeConfirmation = false, autoStart = false, booted = false;
let seenError = '', toastFor = '', toastTimer;
const authViews = ['sign-in', 'verification', 'two-factor', 'mailbox-password'];
const stageViews = { verification: 'verification', 'two-factor': 'two-factor', 'mailbox-password': 'mailbox-password' };
if (preview && !['welcome', ...authViews].includes(view)) { state.accounts = [{ ...sample }]; selectedUser = sample.username; }

const escape = value => String(value ?? '').replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]);
const attr = escape;
const account = () => state.accounts.find(entry => entry.username === selectedUser) || state.accounts[0];
const when = date => !date || date.startsWith('0001-') ? 'Not yet' : new Date(date).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
const button = (label, action, style = 'secondary', extra = '') => `<button class="button ${style}" type="button" data-action="${action}" ${busy ? 'disabled' : ''} ${extra}>${label}</button>`;
const field = (label, id, type = 'text', options = '') => `<div class="field"><label for="${id}">${label}</label><div class="input-wrap"><input id="${id}" name="${id}" type="${type}" ${options} ${busy ? 'disabled' : ''}></div></div>`;
const notice = (text, kind = 'warning') => text ? `<div class="notice ${kind}" role="${kind === 'error' ? 'alert' : 'status'}">${escape(text)}</div>` : '';
const heading = (title, description, eyebrow = '') => `${eyebrow ? `<p class="eyebrow">${eyebrow}</p>` : ''}<h1 class="page-title" tabindex="-1">${title}</h1><p class="page-description">${description}</p>`;
// A failed sign-in is reported once on the sign-in screen; it must not follow the user to every other screen.
const stateError = () => state.error === seenError ? '' : state.error;
// Confirmations float over the panel, so they never push a full screen into scrolling.
const toast = () => message ? `<div class="toast" role="status">${escape(message)}</div>` : '';
const busyText = label => busy ? `<span class="spinner" aria-hidden="true"></span><span>${label}…</span>` : label;

async function call(method, ...args) {
  if (!preview) return runtime.Call.ByName(`main.Desktop.${method}`, ...args);
  // Preview never sends data to a server, accesses the keychain, or writes settings.
  switch (method) {
    case 'State': return structuredClone(state);
    // Like the native backend, these return a new snapshot and leave the current one untouched, so update() sees the change.
    case 'BeginLogin': return { ...structuredClone(state), pendingUsername: sample.username, stage: 'two-factor' };
    case 'CompleteTOTP': case 'CompleteMailboxPassword': case 'ContinueVerification': return { ...structuredClone(state), accounts: [...structuredClone(state.accounts), { ...sample }], pendingUsername: '', stage: 'account' };
    case 'CancelLogin': state.stage = 'account'; return;
    case 'Start': state.running = true; return;
    case 'Stop': state.running = false; return;
    case 'RemoveAccount': state.accounts = []; return;
    case 'RevealPassword': return 'PREVIEW-ONLY-NOT-A-REAL-PASSWORD';
    case 'CopyPassword': message = 'Preview: no password copied.'; return;
    case 'CopyText': message = 'Preview: clipboard unchanged.'; return;
    case 'ConnectionURL': return connectionURL;
    case 'SaveConnectionURL': connectionURL = args[0]; return;
    case 'AutostartEnabled': return autoStart;
    case 'SetAutostart': autoStart = args[0]; return;
    case 'TailscaleStatus': return { installed: true, running: true, httpsReady: false, url: 'https://calendar.example.ts.net/caldav/', message: 'Preview: HTTPS forwarding has not been configured.' };
    case 'CheckConnection': message = 'Preview: no network request was made.'; return;
    case 'OpenVerification': message = 'Preview: the challenge would open in your browser.'; return;
    case 'OpenHelp': message = 'Preview: help would open in your browser.'; return;
    case 'UnlockAccount': return;
    default: throw new Error(`Unavailable preview operation: ${method}`);
  }
}

function update(next) {
  const oldCount = state.accounts.length;
  const oldStage = state.stage;
  state = next;
  if (state.error !== seenError) seenError = '';
  if (stageViews[state.stage] && authViews.includes(view)) view = stageViews[state.stage];
  else if (authViews.includes(view) && state.accounts.length > oldCount) {
    selectedUser = state.accounts.find(entry => !entry.locked)?.username || state.accounts[0]?.username || '';
    view = 'devices';
  } else if (authViews.includes(view) && oldStage !== 'account' && state.stage === 'account') {
    view = 'sign-in'; error = state.error || error || 'The sign-in session ended. Please sign in again.'; seenError = state.error;
  }
  if (!selectedUser && state.accounts.length) selectedUser = state.accounts[0].username;
}

async function perform(task, nextView) {
  if (busy) return;
  busy = true; error = ''; message = ''; render();
  try {
    await task();
    if (nextView) navigate(nextView);
  } catch (err) { error = err?.message || String(err); }
  finally { busy = false; render(); }
}

function navigate(next) {
  clearTimeout(revealTimer); revealed = ''; error = ''; message = ''; removeConfirmation = false;
  view = next; render();
}

function brand() {
  const steps = ['Connect your account', 'Choose your connection', 'Set up your calendar app'];
  const index = ['welcome', ...authViews].includes(view) ? 0 : ['devices', 'access', 'tailscale-help', 'tailscale-forward', 'https-address'].includes(view) ? 1 : 2;
  return `<aside class="brand-panel"><div class="brand-hero"><img class="brand-logo" src="/assets/ioxide-logo.png" alt="iOxide"><h2>Your calendars.<br>Your calendar app.</h2><p class="brand-copy">Bring Proton Calendar to the calendar app you already use. iOxide handles the connection in the background.</p></div><ol class="step-list">${steps.map((step, i) => `<li class="step-item ${i === index ? 'active' : i < index ? 'done' : ''}"><span class="step-number">${i < index ? '✓' : i + 1}</span><span class="step-label">${step}</span></li>`).join('')}</ol><p class="brand-footer">Independent software. Not affiliated with Proton.</p></aside>`;
}

function welcome() {
  return `${heading('Welcome to iOxide', 'Connect your account once. Then use Apple Calendar, Thunderbird, DAVx⁵, or another CalDAV app.', 'CALENDAR BRIDGE')}<div class="welcome-steps"><div><span class="welcome-number">1</span><div><strong>Connect Proton Calendar</strong><p>Sign in securely and unlock your calendars.</p></div></div><div><span class="welcome-number">2</span><div><strong>Connect your calendar app</strong><p>Use your generated iOxide bridge password.</p></div></div></div><div class="actions">${button('Start setup', 'sign-in', 'primary')}${state.accounts.length ? button('Open dashboard', 'dashboard', 'ghost') : ''}</div><p class="hint">iOxide must keep running for your calendar app to sync.</p>`;
}

function signIn() {
  return `${heading('Connect your account', 'Enter your Proton account details.', 'STEP 1 OF 3')}<form id="sign-in-form" class="form-stack">${field('Email or username', 'username', 'text', `autocomplete="username" maxlength="320" required value="${attr(selectedUser)}"`)}${field('Password', 'password', 'password', 'autocomplete="current-password" maxlength="4096" required')}<button class="button primary" type="submit" ${busy || !state.running ? 'disabled' : ''}>${busyText('Sign in')}</button></form><div class="actions">${button('Back', 'welcome', 'ghost')}</div>${!state.running ? notice('Start the local bridge before signing in. If another iOxide instance is running, stop it first.') + button('Start bridge', 'start') : ''}<p class="hint">Your calendar app will use a separate bridge password. Your Proton password is never sent to your calendar app.</p>`;
}

function verification() {
  return `${heading('Verify your sign-in', 'Proton wants to confirm that a person is signing in.')}<ol class="guide"><li>Open Proton's verification page and complete the challenge.</li><li>When the challenge is done, return here and continue.</li></ol><div class="actions">${button('Open verification in browser', 'verify', 'primary')}</div><div class="actions"><button class="button secondary" type="button" data-action="verify-continue" ${busy ? 'disabled' : ''}>${busyText('I have completed verification')}</button>${button('Cancel sign-in', 'cancel-login', 'ghost')}</div><p class="hint">This sign-in session expires after five minutes.</p>`;
}

function codeForm(mailbox = false) {
  return `${heading(mailbox ? 'Unlock your calendars' : 'Two-factor authentication', mailbox ? 'Your account uses a separate mailbox password. Enter it to unlock your calendar keys.' : 'Enter the six-digit code from your authenticator app.')}<p class="hint">${escape(state.pendingUsername || selectedUser)}</p><form id="${mailbox ? 'mailbox' : 'totp'}-form" class="form-stack">${mailbox ? field('Mailbox password', 'mailbox-password', 'password', 'autocomplete="current-password" maxlength="4096" required') : field('Authenticator code', 'code', 'text', 'inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required')}<button class="button primary" type="submit" ${busy ? 'disabled' : ''}>${busyText(mailbox ? 'Unlock calendars' : 'Authenticate')}</button></form><div class="actions">${button('Cancel sign-in', 'cancel-login', 'ghost')}</div>`;
}

function devices() {
  return `${heading('Where will you use your calendar?', 'Choose where your calendar app will connect from.', 'STEP 2 OF 3')}<div class="choice-list">${choice('This computer', 'Keep the connection local.', 'local', 'desktop')}${choice('A phone or another device', 'Set up a private HTTPS connection.', 'remote', 'phone')}</div><p class="hint">The computer running iOxide must stay on and connected. A laptop that sleeps cannot serve your phone.</p><div class="actions">${button('Set up later', 'dashboard', 'ghost')}</div>`;
}

function choice(title, description, action, icon = 'calendar', selected = false) {
  const icons = { desktop: '▣', phone: '▯', calendar: '▦', network: '↔' };
  const appIcons = { 'client-apple': 'apple-calendar.jpg', 'client-thunderbird': 'thunderbird.png', 'client-davx': 'davx5.svg', 'client-other': 'caldav.svg' };
  const image = appIcons[action];
  return `<button class="choice-card ${selected ? 'selected' : ''}" type="button" data-action="${action}" ${busy ? 'disabled' : ''}><span class="choice-icon ${image ? 'app-icon' : ''}" aria-hidden="true">${image ? `<img src="/assets/${image}" alt="">` : icons[icon]}</span><span class="choice-copy"><strong>${title}</strong><span>${description}</span></span><span class="choice-arrow" aria-hidden="true">›</span></button>`;
}

function access() {
  return `${heading('Connect your other devices', 'Choose how your phone or another computer will reach iOxide. Keep this computer on and connected.')}<div class="choice-list">${choice('Set up Tailscale', 'Private HTTPS access between your devices.', 'tailscale-help', 'network')}${choice('I already have an HTTPS address', 'Use Tailscale Serve or an existing reverse proxy.', 'https-address', 'network')}</div><p class="hint">No router port forwarding is needed for the Tailscale option.</p><div class="actions">${button('Back', 'devices', 'ghost')}${button('Set up later', 'dashboard', 'ghost')}</div>`;
}

function tailscaleHelp() {
  const status = network?.message || 'Your Tailscale connection has not been checked yet.';
  return `${heading('Set up Tailscale', 'A private HTTPS connection between your devices.')}<div class="setup-checklist"><div><span class="welcome-number">1</span><div><strong>Connect both devices</strong><p>Install Tailscale and sign in to the same network.</p></div>${button('Install', 'help-tailscale', 'ghost small')}</div><div><span class="welcome-number">2</span><div><strong>Enable HTTPS</strong><p>Turn on MagicDNS and HTTPS certificates.</p></div>${button('Guide', 'help-https', 'ghost small')}</div><div><span class="welcome-number">3</span><div><strong>Forward to iOxide</strong><p>Connect your HTTPS address to the local bridge.</p></div>${button('Set up', 'tailscale-forward', 'ghost small')}</div></div><div class="network-check"><p>${escape(status)}</p>${button('Check connection', 'check-tailscale', 'secondary')}</div><div class="actions">${button('Back', 'access', 'ghost')}${button('Test server address', 'https-address', 'primary')}</div>`;
}

function tailscaleForward() {
  return `${heading('Forward HTTPS to iOxide', 'Run this once in a terminal on the computer running iOxide.')}<div class="command-block"><code>tailscale serve --bg http://127.0.0.1:8081</code></div><div class="actions">${button('Copy command', 'copy-serve', 'primary')}</div><p class="hint">The local bridge stays on port 8081. Tailscale provides the HTTPS address for your other devices.</p>${notice('Already using Tailscale Serve? Review your existing routes before running this command.')}<div class="toolbar">${button('Serve documentation', 'help-serve', 'ghost small')}</div><p class="hint">HTTPS certificate hostnames appear in public certificate logs.</p><div class="actions">${button('Back', 'tailscale-help', 'ghost')}${button('Test server address', 'https-address', 'primary')}</div>`;
}

function httpsAddress() {
  return `${heading('Test your HTTPS address', 'Enter the address your calendar app will use.')}<form id="connection-form" class="form-stack">${field('HTTPS CalDAV address', 'connection-url', 'url', `placeholder="https://your-server.example/caldav/" value="${attr(connectionURL || network?.url || '')}" required`)}<button class="button primary" type="submit" ${busy ? 'disabled' : ''}>${busyText('Test address and continue')}</button></form><p class="hint">This checks HTTPS access from this computer. It cannot confirm that your phone is connected to Tailscale.</p><div class="actions">${button('Back', 'access', 'ghost')}${button('Set up later', 'dashboard', 'ghost')}</div>`;
}

function clients() {
  return `${heading('Choose your calendar app', 'We will show the connection details and instructions for your app.', 'STEP 3 OF 3')}<div class="choice-list">${choice('Apple Calendar', 'iPhone, iPad and Mac', 'client-apple')}${choice('Thunderbird', 'Windows, macOS and Linux', 'client-thunderbird')}${choice('DAVx⁵', 'Calendar sync on Android', 'client-davx')}${choice('Other CalDAV app', 'Manual setup details', 'client-other')}</div><div class="actions">${button('Back', 'devices', 'ghost')}${button('Set up later', 'dashboard', 'ghost')}</div>`;
}

function detail(label, value, action) {
  return `<div class="detail-row"><div><span class="detail-label">${label}</span><span class="detail-value" title="${attr(value)}">${escape(value)}</span></div>${action ? button('Copy', action, 'ghost small', `aria-label="Copy ${attr(label.toLowerCase())}"`) : ''}</div>`;
}

function detailsCard() {
  const user = account()?.username || '';
  return `<section class="detail-card"><h2 class="section-title">CalDAV connection</h2>${detail('Server address', connectionURL || state.address, 'copy-url')}${detail('Username', user, 'copy-user')}<div class="detail-row"><div><span class="detail-label">Bridge password</span><span class="detail-value password-value">${revealed ? escape(revealed) : '••••••••••••••••'}</span></div><div class="inline-actions">${button(revealed ? 'Hide' : 'Reveal', 'reveal', 'ghost small')}${button('Copy', 'copy-password', 'ghost small', 'aria-label="Copy bridge password"')}</div></div>${detail('Security', connectionURL ? 'HTTPS' : 'Localhost HTTP — this computer only')}</section>`;
}

function clientGuide() {
  const guides = {
    apple: ['Add a CalDAV account in Calendar account settings.', 'Choose manual or advanced setup if prompted, then enter the details below.', 'On iPhone or iPad, use your HTTPS address and keep Tailscale connected.'],
    thunderbird: ['In Calendar, choose to create a new calendar on the network.', 'Enter the username and server address below, then use the bridge password when asked.', 'Select the calendars you want to sync.'],
    davx: ['In DAVx⁵, add an account using a URL and username.', 'Enter your HTTPS address, username and bridge password below.', 'Enable calendar sync and select the calendars you want.'],
    other: ['Add a CalDAV account in your calendar app.', 'Enter the server address, username and bridge password below.', 'Choose which calendars to display.']
  };
  const needsHTTPS = (client === 'apple' || client === 'davx') && !connectionURL;
  return `${heading('Set up your calendar app', 'Add a CalDAV account using your iOxide connection details.')}<ol class="guide">${guides[client].map(line => `<li>${line}</li>`).join('')}</ol>${needsHTTPS ? notice('For a phone or tablet, set up HTTPS access first. Apple Calendar may also require HTTPS on this computer.') : ''}<div class="actions">${button('Show connection details', 'connection', 'primary')}</div><div class="actions">${needsHTTPS ? button('Set up HTTPS', 'access', 'ghost') : ''}${client !== 'other' ? button('App website', 'help-'+client, 'ghost') : ''}${button('Choose another app', 'clients', 'ghost')}</div>`;
}

function connection() {
  return `${heading('Your connection details', 'Use this bridge password in your calendar app, not your Proton password.')}${detailsCard()}<p class="hint">Copied passwords clear after 45 seconds if the clipboard has not changed.</p><div class="actions">${button('Check setup', 'check', 'primary')}${button('Setup instructions', 'client-guide', 'ghost')}</div>`;
}

function check() {
  const entry = account();
  const loaded = entry?.listed && entry.loaded === entry.calendars;
  const connected = entry?.lastClient && !entry.lastClient.startsWith('0001-');
  return `${heading(connected ? 'Your calendar app has connected' : 'Your bridge is ready', connected ? 'iOxide received a successful authenticated request from a calendar app.' : 'Finish adding the account in your calendar app. We will show when its first authenticated request arrives.')}<div class="check-list"><p><span>${state.running ? '✓' : '○'}</span>Local bridge ${state.running ? 'running' : 'stopped'}</p><p><span>${loaded ? '✓' : '○'}</span>${entry?.listed ? `${entry.loaded} of ${entry.calendars} calendars loaded` : 'Loading calendar information'}</p><p><span>${connected ? '✓' : '○'}</span>${connected ? 'Calendar app connected' : 'Waiting for your calendar app'}</p></div>${entry?.syncError ? notice(entry.syncError, 'error') : ''}<div class="actions">${button('Open dashboard', 'dashboard', 'primary')}${button('Show connection details', 'connection', 'ghost')}</div>`;
}

function dashboardContent() {
  const entry = account();
  if (!entry) return `${heading('No accounts connected', 'Add your Proton Calendar account to start syncing.')}<div class="actions">${button('Add account', 'sign-in', 'primary')}</div>`;
  if (entry.locked) return `${heading('Unlock your account', 'This account was added using the command line or its saved bridge password is unavailable.')}<form id="unlock-form" class="form-stack">${field('Existing bridge password', 'bridge-password', 'password', 'autocomplete="off" required')}<button class="button primary" type="submit" ${busy ? 'disabled' : ''}>${busyText('Unlock account')}</button></form><p class="hint">Use the password generated by iOxide, not your Proton password. It will be saved in your operating system credential store.</p>${removeControls()}`;
  return `<header class="dashboard-header"><div><p class="eyebrow">ACCOUNT</p><h1 class="page-title" tabindex="-1">${escape(entry.username)}</h1><p class="page-description">${entry.listed ? `${entry.loaded} of ${entry.calendars} calendars loaded · ${entry.events} cached events` : 'Loading your calendars…'}</p></div><span class="status-pill ${state.running ? 'good' : 'muted'}">${state.running ? 'Bridge running' : 'Bridge stopped'}</span></header>${notice(entry.error || entry.syncError, 'error')}<section class="detail-card"><h2 class="section-title">Calendar app connection</h2>${detail('Server address', connectionURL || state.address, 'copy-url')}<div class="toolbar">${button('Connection details', 'connection', 'primary')}${button('Setup instructions', 'clients', 'ghost')}</div></section><div class="sync-summary"><div><span class="detail-label">Last successful sync</span><span>${when(entry.lastSync)}</span></div><div><span class="detail-label">Last calendar app connection</span><span>${when(entry.lastClient)}</span></div></div>${removeControls()}`;
}

function removeControls() {
  if (removeConfirmation) return `<section class="notice warning"><p>Remove this account from this computer, including the local CLI, and delete its saved bridge password? Your calendars in Proton will not be deleted.</p><div class="toolbar">${button('Remove account', 'remove-confirm', 'danger')}${button('Keep account', 'remove-cancel', 'ghost')}</div></section>`;
  return `<div class="account-actions">${button('Remove account from iOxide', 'remove', 'ghost small')}</div>`;
}

function settings() {
  return `<header class="dashboard-header"><div>${heading('Settings', 'Control how iOxide runs and how your other devices connect.')}</div><span class="status-pill ${state.running ? 'good' : 'muted'}">${state.running ? 'Bridge running' : 'Bridge stopped'}</span></header><div class="settings-row"><div><h2 class="section-title">Start at login</h2><p>Keep iOxide available after you sign in to this computer.</p></div><label class="toggle"><input id="autostart" type="checkbox" ${autoStart ? 'checked' : ''} ${busy ? 'disabled' : ''}><span>Enabled</span></label></div><div class="settings-row"><div><h2 class="section-title">Background bridge</h2><p>Closing the window keeps iOxide in the tray. Quit from the tray to stop it.</p></div>${button(state.running ? 'Stop bridge' : 'Start bridge', state.running ? 'stop' : 'start')}</div><div class="detail-card"><h2 class="section-title">Other devices</h2><p>${escape(connectionURL || 'No HTTPS address configured. The bridge is available on this computer only.')}</p><div class="toolbar">${button('Configure secure access', 'access')}${connectionURL ? button('Use local connection', 'clear-url', 'ghost') : ''}</div></div><div class="settings-row"><p class="hint">Passwords are kept in your OS credential store. Proton sessions are encrypted locally.</p>${button('Help', 'help-help', 'ghost')}</div>`;
}

function dashboardShell(content) {
  return `<div class="dashboard"><aside class="sidebar"><div class="sidebar-header"><div class="sidebar-brand"><img src="/assets/ioxide-logo.png" alt="iOxide"></div></div><h2 class="sidebar-heading">Accounts</h2><nav aria-label="Accounts">${state.accounts.map(entry => `<button class="account-item ${entry.username === account()?.username ? 'selected' : ''}" type="button" data-user="${attr(entry.username)}"><span class="account-avatar" aria-hidden="true">${escape(entry.username.slice(0, 1).toUpperCase())}</span><span class="account-copy"><strong>${escape(entry.username)}</strong><span>${entry.locked ? 'Locked' : entry.syncError || entry.error ? 'Needs attention' : !state.running ? 'Stopped' : !entry.listed || entry.loaded < entry.calendars ? 'Loading calendars' : 'Calendars ready'}</span></span></button>`).join('')}</nav><div class="sidebar-footer">${button('+ Add account', 'sign-in', 'ghost')}${button('Settings', 'settings', 'ghost')}${button('Help', 'help-help', 'ghost')}<p>Independent software.<br>Not affiliated with Proton.</p></div></aside><main class="dashboard-main">${notice(error || stateError(), 'error')}${content}${toast()}</main></div>`;
}

function render(preserveScroll = false) {
  const focusedAction = preserveScroll ? document.activeElement?.dataset?.action : '';
  const focusedUser = preserveScroll ? document.activeElement?.dataset?.user : '';
  const scroll = preserveScroll ? [root.querySelector('.content-panel')?.scrollTop || 0, root.querySelector('.dashboard-main')?.scrollTop || 0] : [0, 0];
  const screens = { welcome, 'sign-in': signIn, verification, 'two-factor': () => codeForm(), 'mailbox-password': () => codeForm(true), devices, access, 'tailscale-help': tailscaleHelp, 'tailscale-forward': tailscaleForward, 'https-address': httpsAddress, clients, 'client-guide': clientGuide, connection, check };
  if (view === 'dashboard' || view === 'settings') root.innerHTML = dashboardShell(view === 'settings' ? settings() : dashboardContent());
  else root.innerHTML = `<div class="app-shell">${brand()}<main class="content-panel"><div class="content-body">${notice(error || stateError(), 'error')}${(screens[view] || welcome)()}</div>${toast()}</main></div>`;
  root.setAttribute('aria-busy', String(busy));
  if (!message) { clearTimeout(toastTimer); toastFor = ''; }
  else if (toastFor !== message) { toastFor = message; clearTimeout(toastTimer); toastTimer = setTimeout(() => { message = ''; render(true); }, 6000); }
  if (focusedAction || focusedUser) [...root.querySelectorAll('button')].find(el => focusedAction ? el.dataset.action === focusedAction : el.dataset.user === focusedUser)?.focus({preventScroll:true});
  if (root.querySelector('.content-panel')) root.querySelector('.content-panel').scrollTop = scroll[0];
  if (root.querySelector('.dashboard-main')) root.querySelector('.dashboard-main').scrollTop = scroll[1];
  if (preview) root.insertAdjacentHTML('beforeend', `<nav class="preview-bar" aria-label="Preview screens"><strong>PREVIEW · sample data only</strong><label for="preview-screen">Screen</label><select id="preview-screen">${['welcome', 'sign-in', 'verification', 'two-factor', 'mailbox-password', 'devices', 'access', 'tailscale-help', 'tailscale-forward', 'https-address', 'clients', 'client-guide', 'connection', 'check', 'dashboard', 'settings'].map(screen => `<option ${screen === view ? 'selected' : ''}>${screen}</option>`).join('')}</select></nav>`);
}

root.addEventListener('click', event => {
  const target = event.target.closest('button');
  if (!target || busy) return;
  if (preview && target.dataset.preview) { if (!state.accounts.length && !authViews.includes(target.dataset.preview) && target.dataset.preview !== 'welcome') { state.accounts = [{ ...sample }]; selectedUser = sample.username; } navigate(target.dataset.preview); return; }
  if (target.dataset.user) { selectedUser = target.dataset.user; navigate('dashboard'); return; }
  const action = target.dataset.action;
  if (!action) return;
  if (action.startsWith('help-')) { perform(() => call('OpenHelp', action.slice(5))); return; }
  if (['client-apple', 'client-thunderbird', 'client-davx', 'client-other'].includes(action)) { client = action.slice(7); navigate('client-guide'); return; }
  const routes = ['welcome', 'sign-in', 'dashboard', 'devices', 'access', 'tailscale-help', 'tailscale-forward', 'https-address', 'clients', 'client-guide', 'connection'];
  if (routes.includes(action)) { if (action === 'sign-in') selectedUser = ''; navigate(action); return; }
  switch (action) {
    case 'check': perform(async () => update(await call('State')), 'check'); break;
    case 'settings': perform(async () => { autoStart = await call('AutostartEnabled'); }, 'settings'); break;
    case 'local': perform(async () => { remote = false; connectionURL = ''; await call('SaveConnectionURL', ''); }, 'clients'); break;
    case 'remote': remote = true; navigate('access'); break;
    case 'verify': perform(() => call('OpenVerification')); break;
    case 'verify-continue': perform(async () => update(await call('ContinueVerification'))); break;
    case 'cancel-login': perform(async () => { await call('CancelLogin'); state = await call('State'); }, 'sign-in'); break;
    case 'start': perform(async () => { await call('Start'); update(await call('State')); }); break;
    case 'stop': perform(async () => { await call('Stop'); update(await call('State')); }); break;
    case 'check-tailscale': perform(async () => { network = await call('TailscaleStatus'); }); break;
    case 'copy-serve': perform(async () => { await call('CopyText', 'tailscale serve --bg http://127.0.0.1:8081'); if (!preview) message = 'Command copied.'; }); break;
    case 'copy-url': perform(async () => { await call('CopyText', connectionURL || state.address); if (!preview) message = 'Server address copied.'; }); break;
    case 'copy-user': perform(async () => { await call('CopyText', account()?.username || ''); if (!preview) message = 'Username copied.'; }); break;
    case 'copy-password': perform(async () => { await call('CopyPassword', account()?.username || ''); if (!preview) message = 'Bridge password copied. Clipboard clears in 45 seconds if unchanged.'; }); break;
    case 'reveal': if (revealed) { revealed = ''; clearTimeout(revealTimer); render(true); } else perform(async () => { revealed = await call('RevealPassword', account()?.username || ''); clearTimeout(revealTimer); revealTimer = setTimeout(() => { revealed = ''; render(true); }, 20000); }); break;
    case 'remove': removeConfirmation = true; render(); break;
    case 'remove-cancel': removeConfirmation = false; render(); break;
    case 'remove-confirm': perform(async () => { await call('RemoveAccount', account()?.username || ''); state = await call('State'); selectedUser = state.accounts[0]?.username || ''; }, 'dashboard'); break;
    case 'clear-url': perform(async () => { await call('SaveConnectionURL', ''); connectionURL = ''; message = 'Other-device address removed.'; }); break;
  }
});

root.addEventListener('submit', event => {
  event.preventDefault();
  if (busy) return;
  const form = event.target;
  const values = new FormData(form);
  const value = key => String(values.get(key) || '');
  if (form.id === 'sign-in-form') {
    const username = value('username'), password = value('password');
    selectedUser = username; form.reset(); values.delete('password');
    perform(async () => update(await call('BeginLogin', username, password)));
  } else if (form.id === 'totp-form') {
    const code = value('code'); form.reset();
    perform(async () => update(await call('CompleteTOTP', code)));
  } else if (form.id === 'mailbox-form') {
    const password = value('mailbox-password'); form.reset(); values.delete('mailbox-password');
    perform(async () => update(await call('CompleteMailboxPassword', password)));
  } else if (form.id === 'unlock-form') {
    const password = value('bridge-password'); form.reset(); values.delete('bridge-password');
    perform(async () => { await call('UnlockAccount', account()?.username || '', password); update(await call('State')); });
  } else if (form.id === 'connection-form') {
    const url = value('connection-url');
    perform(async () => { await call('CheckConnection', url); await call('SaveConnectionURL', url); connectionURL = preview ? url : await call('ConnectionURL'); remote = true; }, 'clients');
  }
});

root.addEventListener('change', event => {
  if (preview && event.target.id === 'preview-screen') { if (!state.accounts.length && !authViews.includes(event.target.value) && event.target.value !== 'welcome') { state.accounts = [{ ...sample }]; selectedUser = sample.username; } navigate(event.target.value); return; }
  if (event.target.id === 'autostart') {
    const enabled = event.target.checked;
    perform(async () => { await call('SetAutostart', enabled); autoStart = enabled; });
  }
});

async function refresh(initial = false) {
  if (busy) return;
  try {
    const next = await call('State');
    if (busy) return; // An action started while this poll was in flight; its own result is newer.
    const oldView = view;
    update(next);
    if (initial) {
      connectionURL = await call('ConnectionURL');
      if (state.accounts.length) view = 'dashboard';
    }
    const editing = [...root.querySelectorAll('form input')].some(input => input.value !== '' || input === document.activeElement);
    if (initial || view !== oldView || ['dashboard', 'check', 'verification'].includes(view) && !editing) render(view === oldView);
    booted = true;
  } catch { if (!booted) { error = 'Could not connect to the desktop bridge. Restart iOxide and try again.'; render(); } }
}

if (preview) render();
else {
  runtime.Events.On('navigate', event => {
    const requested = event.data;
    if (requested === 'settings') perform(async () => { autoStart = await call('AutostartEnabled'); }, 'settings');
    else if (['dashboard', 'connection'].includes(requested)) navigate(state.accounts.length ? requested : 'welcome');
  });
  await refresh(true);
  setInterval(() => refresh(), 2500);
}
