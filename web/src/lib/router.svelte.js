// Minimal hash router: #/smb/shares -> "/smb/shares".
function current() {
  return location.hash.replace(/^#/, '') || '/';
}

export const route = $state({ path: current() });

window.addEventListener('hashchange', () => {
  route.path = current();
});

export function navigate(path) {
  location.hash = path;
}
