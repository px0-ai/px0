// web/src/table.js

/* CSV and TSV tabs open as a table in the preview overlay (markdown.js owns the
   overlay, the switch and navigation). The server parses the file and returns
   plain strings; every cell goes in as textContent, so nothing in a file can
   become markup. Rows carry their source line (data-line), the same contract
   Markdown blocks follow, so find, go-to-line and the source toggle line up. */

function cell(tag, text, cls) {
  const el = document.createElement(tag);
  if (cls) el.className = cls;
  if (text) el.textContent = text;
  return el;
}

/** @param {{header: string[], headerLine: number, rows: {line: number, cells: string[]}[], cols: number, truncated: boolean}} j
 *  @param {number} totalLines line count of the whole file, for the footer */
export function buildTable(j, totalLines) {
  const frag = document.createDocumentFragment();
  if (!j.cols) {
    frag.append(cell('p', 'This file is empty.', 'csv-empty'));
    return frag;
  }
  const table = document.createElement('table');
  table.className = 'csv-t';

  const hr = document.createElement('tr');
  hr.dataset.line = String(j.headerLine || 1);
  hr.append(cell('th', String(j.headerLine || 1), 'ln'));
  for (let i = 0; i < j.cols; i++) hr.append(cell('th', j.header[i] || ''));
  const thead = document.createElement('thead');
  thead.append(hr);

  const tbody = document.createElement('tbody');
  const hw = j.header.length;
  for (const r of j.rows) {
    const tr = document.createElement('tr');
    tr.dataset.line = String(r.line);
    tr.append(cell('td', String(r.line), 'ln'));
    // A short row leaves hatched gaps under the header; a long one runs on under blank headings.
    for (let i = 0; i < j.cols; i++) tr.append(cell('td', r.cells[i] || '', i >= r.cells.length && i < hw ? 'miss' : ''));
    tbody.append(tr);
  }
  table.append(thead, tbody);
  frag.append(table);

  if (j.truncated) {
    const cap = cell('div', '', 'csv-cap');
    cap.append(
      'Showing the first ' + j.rows.length.toLocaleString() + ' rows of a ' +
        totalLines.toLocaleString() + '-line file. ',
      cell('button', 'Open Source', 'csv-open-source'),
      ' to see the rest.'
    );
    frag.append(cap);
  }
  return frag;
}
