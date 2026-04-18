/* OpenScape — app.js
   Minimal vanilla JS: drag-reorder init, upload zone drag-and-drop
*/

document.addEventListener('DOMContentLoaded', function () {
  initSortable();
  initUploadForm();
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

/* ── Per-file XHR upload with progress bars ── */
function initUploadForm() {
  var form = document.getElementById('upload-form');
  if (!form) return;

  form.addEventListener('submit', function (e) {
    e.preventDefault();

    var input  = form.querySelector('input[type="file"]');
    if (!input || !input.files.length) return;

    var files   = Array.from(input.files);
    var labelEl = document.getElementById('upload-zone-label');
    var listEl  = document.getElementById('upload-file-list');
    var csrf    = form.querySelector('[name="_csrf"]').value;

    if (labelEl) labelEl.style.display = 'none';
    input.disabled = true;

    var rows = files.map(function (file) {
      var item = document.createElement('div');
      item.className = 'upload-file-item';
      item.innerHTML =
        '<div class="upload-file-item__header">' +
          '<span class="upload-file-item__name" title="' + escapeHtml(file.name) + '">' + escapeHtml(file.name) + '</span>' +
          '<span class="upload-file-item__status">Waiting\u2026</span>' +
        '</div>' +
        '<div class="upload-file-item__bar-track">' +
          '<div class="upload-file-item__bar"></div>' +
        '</div>';
      listEl.appendChild(item);
      return {
        file:   file,
        el:     item,
        status: item.querySelector('.upload-file-item__status'),
        bar:    item.querySelector('.upload-file-item__bar'),
      };
    });

    function uploadNext(index) {
      if (index >= rows.length) {
        setTimeout(function () { window.location.reload(); }, 1500);
        return;
      }

      var row = rows[index];
      row.status.textContent = '0%';

      var fd = new FormData();
      fd.append('_csrf', csrf);
      fd.append('photos', row.file);

      var xhr = new XMLHttpRequest();

      xhr.upload.addEventListener('progress', function (e) {
        if (!e.lengthComputable) return;
        var pct = Math.round(e.loaded / e.total * 100);
        row.bar.style.width = pct + '%';
        row.status.textContent = pct + '% \u2014 ' + formatBytes(e.loaded) + ' of ' + formatBytes(e.total);
      });

      xhr.addEventListener('load', function () {
        if (xhr.status >= 200 && xhr.status < 300) {
          row.bar.style.width = '100%';
          row.status.textContent = 'Done';
          row.el.classList.add('upload-file-item--done');
        } else {
          var msg = 'Failed';
          try { msg = JSON.parse(xhr.responseText).error || msg; } catch (_) {}
          row.status.textContent = msg;
          row.el.classList.add('upload-file-item--error');
        }
        uploadNext(index + 1);
      });

      xhr.addEventListener('error', function () {
        row.status.textContent = 'Network error';
        row.el.classList.add('upload-file-item--error');
        uploadNext(index + 1);
      });

      xhr.open('POST', form.action);
      xhr.setRequestHeader('X-CSRF-Token', csrf);
      xhr.send(fd);
    }

    uploadNext(0);
  });
}

function escapeHtml(s) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function formatBytes(bytes) {
  if (bytes < 1024 * 1024) return Math.round(bytes / 1024) + ' KB';
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
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
      if (file.type.startsWith('image/') || file.type.startsWith('video/')) dt.items.add(file);
    }
    if (dt.files.length) {
      input.files = dt.files;
      input.dispatchEvent(new Event('change', { bubbles: true }));
    }
  });
}
