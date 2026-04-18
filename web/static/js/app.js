/* OpenScape — app.js
   Minimal vanilla JS: drag-reorder init, upload zone drag-and-drop
*/

document.addEventListener('DOMContentLoaded', function () {
  initSortable();
  initUploadZone();
  initPhotoFilter();
});

/* ── Drag-to-reorder photo grid ── */
function initSortable() {
  const meta = document.getElementById('sortable-meta');
  const grid = document.getElementById('sortable-grid');
  if (!meta || !grid || typeof Sortable === 'undefined') return;

  const galleryID = meta.dataset.galleryId;
  const csrfToken = meta.dataset.csrf;

  Sortable.create(grid, {
    animation: 200,
    ghostClass: 'photo-card--ghost',
    onEnd: function () {
      const ids = Array.from(grid.querySelectorAll('.photo-card'))
        .map(el => el.dataset.id)
        .filter(Boolean);

      const form = new FormData();
      ids.forEach(id => form.append('order[]', id));

      fetch('/admin/galleries/' + galleryID + '/photos/reorder', {
        method: 'POST',
        body: form,
        headers: { 'X-CSRF-Token': csrfToken }
      });
    }
  });
}

/* ── Content-type filter tabs ── */
function initPhotoFilter() {
  const bar = document.getElementById('photo-filter');
  if (!bar) return;

  bar.addEventListener('click', function (e) {
    const btn = e.target.closest('[data-filter]');
    if (!btn) return;

    bar.querySelectorAll('[data-filter]').forEach(function (b) {
      b.classList.remove('btn--primary');
      b.classList.add('btn--ghost');
    });
    btn.classList.remove('btn--ghost');
    btn.classList.add('btn--primary');

    const filter = btn.dataset.filter;
    document.querySelectorAll('.photo-card').forEach(function (card) {
      card.style.display = (filter === 'all' || card.dataset.type === filter) ? '' : 'none';
    });
  });
}

/* ── Upload zone drag-and-drop ── */
function initUploadZone() {
  const zone = document.getElementById('upload-zone');
  if (!zone) return;

  zone.addEventListener('dragover', function (e) {
    e.preventDefault();
    zone.classList.add('dragover');
  });

  zone.addEventListener('dragleave', function () {
    zone.classList.remove('dragover');
  });

  zone.addEventListener('drop', function (e) {
    e.preventDefault();
    zone.classList.remove('dragover');
    const input = zone.querySelector('input[type="file"]');
    if (!input || !e.dataTransfer.files.length) return;

    // Create a new DataTransfer to set files on the input
    const dt = new DataTransfer();
    for (const file of e.dataTransfer.files) {
      if (file.type.startsWith('image/')) dt.items.add(file);
    }
    if (dt.files.length) {
      input.files = dt.files;
      input.dispatchEvent(new Event('change', { bubbles: true }));
    }
  });
}
