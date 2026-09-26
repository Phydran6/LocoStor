// Applies the saved theme before first paint. A separate file (not inline)
// so the Content-Security-Policy can forbid inline scripts.
try {
  var t = localStorage.getItem('locostor-theme') || 'system';
  var dark = t === 'dark' || (t === 'system' && matchMedia('(prefers-color-scheme: dark)').matches);
  document.documentElement.classList.toggle('dark', dark);
} catch (e) {}
