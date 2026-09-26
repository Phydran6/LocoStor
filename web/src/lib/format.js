export function bytes(n, digits = 1) {
  if (n == null) return '–';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i === 0 ? 0 : digits)} ${units[i]}`;
}

// Disk vendors use decimal units; show capacities the way labels do.
export function capacity(n) {
  if (!n) return '–';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let i = 0;
  let v = n;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v >= 100 ? v.toFixed(0) : v.toFixed(1).replace(/\.0$/, '')} ${units[i]}`;
}

export function duration(seconds) {
  if (seconds == null) return '–';
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

export function hours(h) {
  if (h == null) return '–';
  const years = h / 24 / 365;
  return `${h.toLocaleString()} h` + (years >= 0.1 ? ` (${years.toFixed(1)} y)` : '');
}

export function percent(used, total) {
  if (!total) return 0;
  return Math.round((used / total) * 1000) / 10;
}

export function dateTime(s) {
  if (!s || s.startsWith('0001-')) return '–';
  return new Date(s).toLocaleString();
}

// Map health/state strings to badge tones.
export function tone(state) {
  switch (state) {
    case 'ok':
    case 'active':
    case 'passed':
      return 'ok';
    case 'warning':
    case 'degraded':
    case 'rebuilding':
    case 'activating':
    case 'reloading':
      return 'warn';
    case 'failed':
    case 'faulty':
      return 'bad';
    case 'standby':
    case 'spare':
      return 'info';
    default:
      return 'muted';
  }
}
