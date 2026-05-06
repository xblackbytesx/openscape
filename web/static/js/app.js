/* OpenScape — app.js
   Minimal vanilla JS: drag-reorder init, upload zone drag-and-drop
*/

document.addEventListener('DOMContentLoaded', function () {
  initSortable();
  initUploadForm();
  initUploadZone();
  initPhotoFilter();
  initSortToggle();
  initPhotoNav();
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

/* ── Resumable uploads via tus-js-client ──
   tus.min.js is loaded with `defer` so window.tus may not exist when
   DOMContentLoaded fires. We feature-detect at submit time. If tus is
   unavailable for any reason (script blocked, ancient browser), we fall
   back to a parallel-pool XHR uploader.

   Either path:
     - Parallel pool of UPLOAD_CONCURRENCY simultaneous uploads.
     - "Stalled" badge if no progress event arrives for UPLOAD_STALL_MS
       (server is processing, not actually stuck).
     - beforeunload aborts pending uploads so the server stops working.
     - Page reloads after all files finish so HTMX-polling photo cards
       pick up status updates from the worker pool.

   tus path additionally provides:
     - Resume on connection drop (upload URLs persisted in localStorage).
     - Chunking, so a 4 GB upload that breaks at 3.9 GB doesn't restart. */
// Concurrency 1 by default — sequential uploads with tus resume are the right
// trade-off on mobile. Concurrent tus uploads multiply chunk buffers, progress
// events, and DOM mutations by N, which is enough to push a constrained phone
// browser into "page unresponsive" territory. Throughput per file is unchanged
// because each upload still saturates the link via chunking.
var UPLOAD_CONCURRENCY = 1;
var UPLOAD_STALL_MS = 60000; // 60s — a slow chunk on cellular is normal, don't cry wolf
var TUS_CHUNK_SIZE = 4 * 1024 * 1024; // 4 MB — smaller buffer + faster retry on flaky links

function initUploadForm() {
  var form = document.getElementById('upload-form');
  var zone = document.getElementById('upload-zone');
  if (!form || !zone) return;

  form.addEventListener('submit', function (e) {
    e.preventDefault();

    var input = form.querySelector('input[type="file"]');
    if (!input || !input.files.length) return;

    var files   = Array.from(input.files);
    var labelEl = document.getElementById('upload-zone-label');
    var listEl  = document.getElementById('upload-file-list');
    var csrf    = zone.dataset.csrfToken || form.querySelector('[name="_csrf"]').value;

    if (labelEl) labelEl.style.display = 'none';
    input.disabled = true;

    var rows = files.map(function (file) {
      var item = document.createElement('div');
      item.className = 'upload-file-item';
      item.innerHTML =
        '<div class="upload-file-item__header">' +
          '<span class="upload-file-item__name" title="' + escapeHtml(file.name) + '">' + escapeHtml(file.name) + '</span>' +
          '<span class="upload-file-item__status">Waiting…</span>' +
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
        upload: null,
        done:   false,
      };
    });

    var nextIndex = 0;
    var inFlight  = 0;
    var completed = 0;
    var aborters  = new Set();

    function abortPending() {
      aborters.forEach(function (a) { try { a(); } catch (_) {} });
    }
    window.addEventListener('beforeunload', abortPending, { once: true });

    function setStallWatcher(row, getLastProgress) {
      var t = setInterval(function () {
        if (row.done) { clearInterval(t); return; }
        if (Date.now() - getLastProgress() > UPLOAD_STALL_MS) {
          row.el.classList.add('upload-file-item--stalled');
          row.status.textContent = 'Server processing…';
        }
      }, 5000);
      return t;
    }

    function finishRow(row, success, message) {
      if (row.done) return;
      row.done = true;
      if (success) {
        row.bar.style.width = '100%';
        row.status.textContent = 'Done';
        row.el.classList.add('upload-file-item--done');
      } else {
        row.status.textContent = message || 'Failed';
        row.el.classList.add('upload-file-item--error');
      }
      completed++;
      inFlight--;
      pump();
    }

    function uploadViaTus(row) {
      var lastProgress = Date.now();
      var stallTimer = setStallWatcher(row, function () { return lastProgress; });

      var upload = new tus.Upload(row.file, {
        endpoint: zone.dataset.tusEndpoint,
        chunkSize: TUS_CHUNK_SIZE,
        retryDelays: [0, 1000, 3000, 5000, 10000, 30000],
        storeFingerprintForResuming: true,
        removeFingerprintOnSuccess: true,
        metadata: {
          filename:  row.file.name,
          filetype:  row.file.type || 'application/octet-stream',
          galleryID: zone.dataset.galleryId,
        },
        headers: { 'X-CSRF-Token': csrf },
        onProgress: function (sent, total) {
          lastProgress = Date.now();
          row.el.classList.remove('upload-file-item--stalled');
          var pct = Math.round(sent / total * 100);
          row.bar.style.width = pct + '%';
          row.status.textContent = pct + '% — ' + formatBytes(sent) + ' of ' + formatBytes(total);
        },
        onSuccess: function () {
          clearInterval(stallTimer);
          aborters.delete(abortFn);
          finishRow(row, true);
        },
        onError: function (err) {
          clearInterval(stallTimer);
          aborters.delete(abortFn);
          finishRow(row, false, (err && err.message) || 'Upload failed');
        },
      });
      row.upload = upload;
      function abortFn() { try { upload.abort(true); } catch (_) {} }
      aborters.add(abortFn);
      // Try to resume an incomplete upload from a previous session for this file.
      upload.findPreviousUploads().then(function (previous) {
        if (previous.length) upload.resumeFromPreviousUpload(previous[0]);
        upload.start();
      });
    }

    function uploadViaXHR(row) {
      row.status.textContent = '0%';
      var fd = new FormData();
      fd.append('_csrf', csrf);
      fd.append('photos', row.file);

      var xhr = new XMLHttpRequest();
      xhr.timeout = 0;
      var lastProgress = Date.now();
      var stallTimer = setStallWatcher(row, function () { return lastProgress; });
      function abortFn() { try { xhr.abort(); } catch (_) {} }
      aborters.add(abortFn);

      xhr.upload.addEventListener('progress', function (e) {
        lastProgress = Date.now();
        row.el.classList.remove('upload-file-item--stalled');
        if (!e.lengthComputable) return;
        var pct = Math.round(e.loaded / e.total * 100);
        row.bar.style.width = pct + '%';
        row.status.textContent = pct + '% — ' + formatBytes(e.loaded) + ' of ' + formatBytes(e.total);
      });
      xhr.addEventListener('load', function () {
        clearInterval(stallTimer);
        aborters.delete(abortFn);
        if (xhr.status >= 200 && xhr.status < 300) {
          finishRow(row, true);
        } else {
          var msg = 'Failed';
          try { msg = JSON.parse(xhr.responseText).error || msg; } catch (_) {}
          finishRow(row, false, msg);
        }
      });
      xhr.addEventListener('error', function () {
        clearInterval(stallTimer); aborters.delete(abortFn); finishRow(row, false, 'Network error');
      });
      xhr.addEventListener('abort', function () {
        clearInterval(stallTimer); aborters.delete(abortFn); finishRow(row, false, 'Cancelled');
      });
      xhr.open('POST', zone.dataset.fallbackEndpoint || form.action);
      xhr.setRequestHeader('X-CSRF-Token', csrf);
      xhr.send(fd);
    }

    var useTus = typeof window.tus !== 'undefined' && tus.isSupported && zone.dataset.tusEndpoint;

    function uploadOne(row) {
      if (useTus) uploadViaTus(row);
      else uploadViaXHR(row);
    }

    function pump() {
      while (inFlight < UPLOAD_CONCURRENCY && nextIndex < rows.length) {
        inFlight++;
        uploadOne(rows[nextIndex++]);
      }
      if (completed >= rows.length) {
        window.removeEventListener('beforeunload', abortPending);
        setTimeout(function () { window.location.reload(); }, 1500);
      }
    }

    pump();
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

/* ── Viewer sort order toggle (client-side, persisted in localStorage) ── */
function initSortToggle() {
  var btn = document.getElementById('viewer-sort-toggle');
  if (!btn) return;

  var grid = document.querySelector('.photo-grid');
  if (!grid) return;

  var STORAGE_KEY = 'openscape-sort-reversed';

  function reverseGrid() {
    var cards = Array.from(grid.children);
    cards.reverse().forEach(function (c) { grid.appendChild(c); });
  }

  function updateLabel(reversed) {
    btn.textContent = reversed ? '↓ Newest first' : '↑ Oldest first';
  }

  var saved = localStorage.getItem(STORAGE_KEY) === '1';
  if (saved) {
    reverseGrid();
    updateLabel(true);
  }

  btn.addEventListener('click', function () {
    var isReversed = localStorage.getItem(STORAGE_KEY) === '1';
    var next = !isReversed;
    localStorage.setItem(STORAGE_KEY, next ? '1' : '0');
    reverseGrid();
    updateLabel(next);
  });
}

/* ── Photo prev/next navigation (arrow keys + swipe) ── */
function initPhotoNav() {
  var prev = document.querySelector('[data-nav="prev"]');
  var next = document.querySelector('[data-nav="next"]');
  if (!prev && !next) return;

  document.addEventListener('keydown', function (e) {
    if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA' || e.target.tagName === 'SELECT') return;
    if (e.key === 'ArrowLeft'  && prev) window.location.href = prev.href;
    if (e.key === 'ArrowRight' && next) window.location.href = next.href;
  });

  var touchStartX = 0, touchStartY = 0;
  document.addEventListener('touchstart', function (e) {
    touchStartX = e.touches[0].clientX;
    touchStartY = e.touches[0].clientY;
  }, { passive: true });
  document.addEventListener('touchend', function (e) {
    var dx = e.changedTouches[0].clientX - touchStartX;
    var dy = e.changedTouches[0].clientY - touchStartY;
    if (Math.abs(dx) > Math.abs(dy) && Math.abs(dx) > 50) {
      if (dx > 0 && prev) window.location.href = prev.href;
      if (dx < 0 && next) window.location.href = next.href;
    }
  }, { passive: true });
}
