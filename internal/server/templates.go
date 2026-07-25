package server

import "html/template"

var webTemplate = template.Must(template.New("web").Parse(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>Kindle Go</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 32px; line-height: 1.45; color: #1f2933; }
    h1 { margin-bottom: 8px; }
    fieldset { border: 1px solid #ccd3dc; padding: 16px; margin: 16px 0; }
    label { display: block; margin: 8px 0; }
    input[type=text], input[type=number], select { min-width: 320px; max-width: 100%; padding: 6px; }
    button { padding: 7px 12px; }
    .record { border-top: 1px solid #d8dee6; padding: 18px 0; }
    .files a { margin-right: 8px; }
    .error { color: #9b1c1c; }
    .muted { color: #627282; }
  </style>
</head>
<body>
  <h1>Kindle Go</h1>
  <p><a href="/tasks">Tasks</a> · <a href="/settings">Settings</a></p>
  <p class="muted">Upload files from this computer, convert when needed, then download from Kindle.</p>
  {{if .Message}}<p><strong>{{.Message}}</strong></p>{{end}}

  <fieldset>
    <legend>Upload</legend>
    <form method="post" action="/books/import" enctype="multipart/form-data">
      <input type="file" name="file" accept=".txt,.epub" required>
      <button type="submit">Import</button>
    </form>
    <p class="muted">TXT up to 32 MiB; EPUB up to 64 MiB and 512 MiB expanded.</p>
  </fieldset>

  {{if .Duplicate.Token}}
  <fieldset>
    <legend>Duplicate source</legend>
    <p><strong>{{.Duplicate.Filename}}</strong> has the same content as an existing book.</p>
    {{range .Duplicate.Existing}}<p>{{.OriginalName}} — {{.UploadedAt}}</p>{{end}}
    <form method="post" action="/books/import/{{.Duplicate.Token}}/confirm">
      <button name="action" value="open" type="submit">Open existing book</button>
      <button name="action" value="import" type="submit">Import as a new book</button>
      <button name="action" value="cancel" type="submit">Cancel</button>
    </form>
  </fieldset>
  {{end}}

  <form method="get" action="/books">
    <input type="text" name="q" value="{{.Query}}" placeholder="Search book names">
    <select name="status"><option value="">All proofread statuses</option><option value="not_started" {{if eq .StatusFilter "not_started"}}selected{{end}}>Not started</option><option value="queued" {{if eq .StatusFilter "queued"}}selected{{end}}>Queued</option><option value="running" {{if eq .StatusFilter "running"}}selected{{end}}>Running</option><option value="completed" {{if eq .StatusFilter "completed"}}selected{{end}}>Completed</option><option value="failed" {{if eq .StatusFilter "failed"}}selected{{end}}>Failed</option><option value="canceled" {{if eq .StatusFilter "canceled"}}selected{{end}}>Canceled</option></select>
    <select name="sort"><option value="">Newest first</option><option value="name_asc" {{if eq .Sort "name_asc"}}selected{{end}}>Name A–Z</option><option value="imported_asc" {{if eq .Sort "imported_asc"}}selected{{end}}>Oldest first</option><option value="status_asc" {{if eq .Sort "status_asc"}}selected{{end}}>Proofread status</option></select>
    <button type="submit">Apply</button>
  </form>
  <p class="muted">{{.Total}} book(s), page {{.Page}}</p>

  {{range .Records}}
  <section class="record">
    <h2><a href="/books/{{.ID}}">{{.OriginalName}}</a></h2>
    <p class="muted">Uploaded {{.UploadedAt}} · proofread: {{.ProofreadStatus}}</p>
    {{if .LastError}}<p class="error">{{.LastError}}</p>{{end}}
    <div class="files">
      {{range .Files}}
        <p><a href="{{.URL}}">{{.Name}}</a> <span class="muted">{{.Kind}}, {{.Format}}, {{.Size}}</span></p>
      {{end}}
    </div>
    <p><a href="/books/{{.ID}}">Open details, generate files, and view tasks</a></p>
  </section>
  {{else}}
  <p>No uploads yet.</p>
  {{end}}
  <p>{{if .HasPrevious}}<a href="/books?q={{urlquery .Query}}&status={{.StatusFilter}}&sort={{.Sort}}&page={{.PreviousPage}}">Previous</a>{{end}} {{if .HasNext}}<a href="/books?q={{urlquery .Query}}&status={{.StatusFilter}}&sort={{.Sort}}&page={{.NextPage}}">Next</a>{{end}}</p>
</body>
</html>`))

var bookTemplate = template.Must(template.New("book").Parse(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>{{.Book.OriginalName}} — Kindle Go</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 32px; line-height: 1.45; color: #1f2933; }
    fieldset { border: 1px solid #ccd3dc; padding: 16px; margin: 18px 0; }
    label { display: block; margin: 8px 0; }
    input[type=text], input[type=number] { min-width: 320px; padding: 6px; }
    table { width: 100%; border-collapse: collapse; margin: 12px 0; }
    th, td { text-align: left; border-bottom: 1px solid #d8dee6; padding: 8px; }
    .error { color: #9b1c1c; }
    .muted { color: #627282; }
    form.inline { display: inline; }
  </style>
</head>
<body>
  <p><a href="/books">← Library</a> · <a href="/tasks">All tasks</a></p>
  <h1>{{.Book.OriginalName}}</h1>
  <p class="muted">Imported {{.Book.UploadedAt}} · {{.Book.InputFormat}}</p>
  {{if .Message}}<p><strong>{{.Message}}</strong></p>{{end}}

  {{if .CanGenerate}}<fieldset>
    <legend>Generate</legend>
    <form method="post" action="/books/{{.Book.ID}}/generate">
      <label>Input file <select name="input_file_id">{{range .GenerationInputs}}<option value="{{.ID}}">{{.Name}} ({{.Role}}{{if .HasUnresolved}}, unresolved locations kept{{end}})</option>{{end}}</select></label>
      <label><input type="checkbox" name="format" value="azw3" checked> AZW3</label>
      {{if eq .Book.InputFormat "txt"}}<label><input type="checkbox" name="format" value="epub"> EPUB</label>{{end}}
      <label>Title <input type="text" name="title" value="{{.Book.TitleDefault}}"></label>
      <label>Author <input type="text" name="author" value="{{.Defaults.Author}}"></label>
      <label>Language <input type="text" name="language" value="{{.Defaults.Language}}"></label>
      <input type="hidden" name="cover_present" value="1"><label><input type="checkbox" name="cover" value="1" {{if .Defaults.Cover}}checked{{end}}> Generate text cover</label>
      {{if eq .Book.InputFormat "txt"}}
      <label>H1 regex <input type="text" name="h1_regex" value="{{.Defaults.TXT.H1Regex}}"></label>
      <label>H2 regex <input type="text" name="h2_regex" value="{{.Defaults.TXT.H2Regex}}"></label>
      <label>Split level <input type="number" name="split_level" min="1" max="2" value="{{.Defaults.TXT.SplitLevel}}"></label>
      <input type="hidden" name="merge_lines_present" value="1"><label><input type="checkbox" name="merge_lines" value="1" {{if .Defaults.TXT.MergeLines}}checked{{end}}> Merge wrapped lines</label>
      <input type="hidden" name="trim_blank_lines_present" value="1"><label><input type="checkbox" name="trim_blank_lines" value="1" {{if .Defaults.TXT.TrimBlankLines}}checked{{end}}> Compress consecutive blank lines</label>
      <label>Drop regexes for this build (one per line)<textarea name="drop_regex" rows="4">{{.DropRegex}}</textarea></label>
      <label>Replacement rules for this build (JSON array)<textarea name="replace_json" rows="4">{{.ReplaceJSON}}</textarea></label>
      <label>Line height <input type="text" name="line_height" value="{{.Defaults.Style.LineHeight}}"></label>
      <label>Paragraph spacing <input type="text" name="paragraph_spacing" value="{{.Defaults.Style.ParagraphSpacing}}"></label>
      <label>Paragraph indent <input type="text" name="paragraph_indent" value="{{.Defaults.Style.ParagraphIndent}}"></label>
      <label>Text align <input type="text" name="text_align" value="{{.Defaults.Style.TextAlign}}"></label>
      {{end}}
      {{if eq .Book.InputFormat "txt"}}<button type="submit" formaction="/books/{{.Book.ID}}/txt-preview">Preview TXT</button>{{end}}
      <button type="submit">Queue generation</button>
    </form>
  </fieldset>{{else}}<p class="error">AZW3 generation is unavailable because this EPUB did not pass compatibility analysis.</p>{{end}}

  <form method="post" action="/books/{{.Book.ID}}/proofreads"><button type="submit">Start AI proofreading with Codex CLI</button></form>

  {{if .ProofreadRuns}}<h2>Proofreading runs</h2>
  <table><thead><tr><th>Completed</th><th>Format</th><th>Model</th><th>Candidates</th></tr></thead><tbody>
  {{range .ProofreadRuns}}<tr><td>{{.CompletedAt}}</td><td>{{.Format}}</td><td>{{if .Model}}{{.Model}}{{else}}Codex default{{end}}</td><td><a href="/books/{{$.Book.ID}}/proofreads/{{.ID}}">Review candidates</a></td></tr>{{end}}
  </tbody></table>{{end}}

  {{with .Compatibility}}
  <h2>EPUB compatibility: {{.Status}}</h2>
  <p>Title: {{.Metadata.Title}} · Author: {{.Metadata.Author}} · Language: {{.Metadata.Language}}</p>
  {{if .Cover.ImageHref}}<p>Cover: {{.Cover.ImageHref}}</p>{{end}}
  <h3>Spine</h3><ul>{{range .Spine}}<li>{{.Href}} — {{.MediaType}} {{if .Title}}— {{.Title}}{{end}}</li>{{else}}<li>No readable spine items.</li>{{end}}</ul>
  <h3>Table of contents</h3><ul>{{range .TOC}}<li>{{.Title}} — {{.Href}}</li>{{else}}<li>No table of contents.</li>{{end}}</ul>
  <h3>Resources</h3><ul>{{range .Resources}}<li>{{.Href}} — {{.MediaType}} — {{.Size}} bytes {{if not .Exists}}(missing){{end}}</li>{{end}}</ul>
  {{if .Issues}}<h3>Blocking issues</h3><ul>{{range .Issues}}<li class="error"><strong>{{.Code}}</strong> [{{.Stage}}] {{.Message}}{{if .Document}} — document: {{.Document}}{{end}}{{if .Resource}} — resource: {{.Resource}}{{end}}</li>{{end}}</ul>{{end}}
  {{end}}

  <h2>Files</h2>
  <table><thead><tr><th>Name</th><th>Role</th><th>Format</th><th>Size</th><th>Created</th></tr></thead><tbody>
  {{range .Files}}<tr><td><a href="{{.URL}}">{{.Name}}</a>{{if .Unresolved}} <span class="error">(contains unresolved source locations)</span>{{end}}{{if .CompatibilityStatus}} <span class="muted">(EPUB compatibility: {{.CompatibilityStatus}})</span>{{end}}{{if .CanDelete}} <form class="inline" method="post" action="/files/{{.ID}}/delete"><button type="submit">Delete</button></form>{{end}}</td><td>{{.Kind}}</td><td>{{.Format}}</td><td>{{.Size}}</td><td>{{.CreatedAt}}</td></tr>{{else}}<tr><td colspan="5">No files.</td></tr>{{end}}
  </tbody></table>

  <h2>Tasks</h2>
  <table><thead><tr><th>Type</th><th>Status</th><th>Stage</th><th>Progress</th><th>Created</th><th>Actions</th></tr></thead><tbody>
  {{range .Tasks}}<tr data-task-id="{{.ID}}" data-task-status="{{.Status}}"><td><a href="/tasks/{{.ID}}">{{.Type}}</a></td><td>{{.Status}}{{if .Error}}<div class="error">{{.Error}}</div>{{end}}</td><td>{{.Stage}}</td><td>{{.Progress}}</td><td>{{.CreatedAt}}</td><td>
    {{if .CanCancel}}<form class="inline" method="post" action="/tasks/{{.ID}}/cancel"><button type="submit">Cancel</button></form>{{end}}
    {{if .CanRetry}}<form class="inline" method="post" action="/tasks/{{.ID}}/retry"><button type="submit">Retry from beginning</button></form>{{end}}
  </td></tr>{{else}}<tr><td colspan="6">No tasks.</td></tr>{{end}}
  </tbody></table>
  <details>
    <summary>Delete this book</summary>
    <p class="error">This permanently deletes the imported copy, all revisions, artifacts, reports, and task history. The file you originally imported from outside this library is not affected.</p>
    <form method="post" action="/books/{{.Book.ID}}/delete"><label><input type="checkbox" name="confirm" value="delete" required> I understand this cannot be undone.</label><button type="submit">Delete book</button></form>
  </details>
  <script>
    const active = [...document.querySelectorAll('[data-task-id]')].filter(row => ['queued','running'].includes(row.dataset.taskStatus));
    if (active.length) setTimeout(async () => {
      const changed = await Promise.all(active.map(async row => {
        const result = await fetch('/tasks/' + row.dataset.taskId + '.json');
        if (!result.ok) return false;
        const task = await result.json();
        return task.status !== row.dataset.taskStatus;
      }));
      if (changed.some(Boolean)) location.reload(); else location.reload();
    }, 2000);
  </script>
</body>
</html>`))

var tasksTemplate = template.Must(template.New("tasks").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Tasks — Kindle Go</title></head><body>
<p><a href="/books">← Library</a></p><h1>Tasks</h1>{{if .Message}}<p>{{.Message}}</p>{{end}}
<table><thead><tr><th>Book</th><th>Type</th><th>Status</th><th>Stage</th><th>Progress</th><th>Created</th></tr></thead><tbody>
{{range .Tasks}}<tr><td><a href="/books/{{.BookID}}">{{.BookID}}</a></td><td><a href="/tasks/{{.ID}}">{{.Type}}</a></td><td>{{.Status}}{{if .Error}} — {{.Error}}{{end}}</td><td>{{.Stage}}</td><td>{{.Progress}}</td><td>{{.CreatedAt}}</td></tr>{{else}}<tr><td colspan="6">No tasks.</td></tr>{{end}}
</tbody></table></body></html>`))

var taskDetailTemplate = template.Must(template.New("task-detail").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Task {{.Task.ID}} — Kindle Go</title><style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 32px; line-height: 1.45; color: #1f2933; }
table { width: 100%; border-collapse: collapse; } th, td { text-align: left; border-bottom: 1px solid #d8dee6; padding: 8px; }
.error { color: #9b1c1c; } .warning { color: #8a5b00; } .muted { color: #627282; }
</style></head><body>
<p><a href="/tasks">← All tasks</a> · <a href="/books/{{.Task.BookID}}">Book</a></p>
<h1>{{.Task.Type}}</h1><p>Status: <strong>{{.Task.Status}}</strong> · stage: {{.Task.Stage}} · progress: {{.Task.Progress}}</p>
{{if .Task.Error}}<p class="error">{{.Task.Error}}</p>{{end}}
<h2>Event timeline</h2><table><thead><tr><th>Time</th><th>Level</th><th>Stage</th><th>Event</th></tr></thead><tbody>
{{range .Events}}<tr><td>{{.CreatedAt}}</td><td class="{{.Level}}">{{.Level}}</td><td>{{.Stage}}</td><td>{{.Message}}</td></tr>{{else}}<tr><td colspan="4">No events.</td></tr>{{end}}
</tbody></table></body></html>`))

var proofreadTemplate = template.Must(template.New("proofread").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Proofreading candidates — Kindle Go</title><style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 32px; line-height: 1.45; color: #1f2933; }
.candidate { border: 1px solid #ccd3dc; border-radius: 6px; padding: 16px; margin: 18px 0; }
.muted { color: #627282; } .error { color: #9b1c1c; } .automatic { color: #176b3a; }
pre { white-space: pre-wrap; background: #f4f6f8; padding: 12px; overflow-wrap: anywhere; }
form.inline { display: inline; } input[type=text] { min-width: 280px; padding: 5px; }
</style></head><body>
<p><a href="/books/{{.BookID}}">← Back to book</a> · <a href="/tasks">All tasks</a></p>
<h1>Proofreading candidates</h1>
<p class="muted">Run {{.Run.ID}} · {{.Run.Format}} · completed {{.Run.CompletedAt}} · model: {{if .Run.Model}}{{.Run.Model}}{{else}}Codex default{{end}}</p>
{{if .Message}}<p><strong>{{.Message}}</strong></p>{{end}}
<form method="get"><label>Filter <select name="filter">
<option value="">All</option><option value="pending" {{if eq .Filter "pending"}}selected{{end}}>Pending</option>
<option value="automatic" {{if eq .Filter "automatic"}}selected{{end}}>Automatic</option>
<option value="accepted" {{if eq .Filter "accepted"}}selected{{end}}>Accepted</option>
<option value="modified" {{if eq .Filter "modified"}}selected{{end}}>Modified</option>
<option value="rejected" {{if eq .Filter "rejected"}}selected{{end}}>Rejected</option>
<option value="conflict" {{if eq .Filter "conflict"}}selected{{end}}>Conflicts</option>
</select></label><button type="submit">Apply</button></form>
<p class="muted">{{.Total}} candidate(s), page {{.Page}}</p>
<form method="post" action="/proofreads/{{.Run.ID}}/revisions">
{{if .Unresolved}}<p><strong>{{.Unresolved}} unresolved candidate(s)</strong> will keep their exact source text.</p><label><input type="checkbox" name="confirm_unresolved" value="1" required> Generate anyway and keep every unresolved location unchanged.</label>{{end}}
<button type="submit">Queue immutable revised file, report, and audit</button></form>
{{range .Candidates}}<section class="candidate" id="{{.Candidate.ID}}">
<h2>{{.Candidate.ExpectedOriginal}} → {{.Candidate.FirstReplacement}}</h2>
<p><strong>Status:</strong> <span class="{{if eq .Outcome "automatic"}}automatic{{end}}">{{.Outcome}}</span> · <strong>Category:</strong> {{.Candidate.Category}} · <strong>Location:</strong> {{.Location}}</p>
{{if .ConflictIDs}}<p class="error"><strong>Conflict:</strong> overlaps applied candidate(s) {{range .ConflictIDs}}<a href="#{{.}}">{{.}}</a> {{end}}. Reject one before applying the other.</p>{{end}}
<p>First review: <strong>{{.Candidate.FirstConfidence}}</strong>, proposed <code>{{.Candidate.FirstReplacement}}</code> — {{.Candidate.Reason}}</p>
<p>Isolated verification: <strong>{{.Candidate.Verification}}</strong>, proposed <code>{{.Candidate.VerifiedReplacement}}</code></p>
<pre>{{.Context}}</pre>
<form class="inline" method="post" action="/candidates/{{.Candidate.ID}}/decision"><input type="hidden" name="decision" value="accept"><button type="submit">Accept first proposal</button></form>
<form class="inline" method="post" action="/candidates/{{.Candidate.ID}}/decision"><input type="hidden" name="decision" value="reject"><button type="submit">Reject / keep original</button></form>
{{if eq .Candidate.Kind "text"}}<form method="post" action="/candidates/{{.Candidate.ID}}/decision"><input type="hidden" name="decision" value="modify"><label>Modified replacement <input type="text" name="replacement" value="{{.Candidate.FirstReplacement}}" required></label><button type="submit">Save modified replacement</button></form>{{end}}
{{if .History}}<details><summary>Decision history ({{len .History}})</summary><ol>{{range .History}}<li>{{.CreatedAt}} — {{.Decision}}{{if .Replacement}}: <code>{{.Replacement}}</code>{{end}}</li>{{end}}</ol></details>{{end}}
</section>{{else}}<p>No candidates match this filter.</p>{{end}}
<p>{{if .HasPrevious}}<a href="?filter={{urlquery .Filter}}&page={{.PreviousPage}}">Previous</a>{{end}} {{if .HasNext}}<a href="?filter={{urlquery .Filter}}&page={{.NextPage}}">Next</a>{{end}}</p>
</body></html>`))

var settingsTemplate = template.Must(template.New("settings").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Settings — Kindle Go</title><style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 32px; line-height: 1.45; color: #1f2933; }
fieldset { border: 1px solid #ccd3dc; padding: 16px; margin: 18px 0; }
label { display: block; margin: 8px 0; } input[type=text], input[type=number], textarea { min-width: 420px; max-width: 100%; padding: 6px; }
.muted { color: #627282; }
</style></head><body>
<p><a href="/books">← Library</a></p><h1>Global settings</h1>{{if .Message}}<p><strong>{{.Message}}</strong></p>{{end}}
<fieldset><legend>Runtime (startup only)</legend>
<p>Library: {{.Runtime.LibraryDir}} <span class="muted">({{.Runtime.LibrarySource}})</span></p>
<p>Web UI: {{.Runtime.WebAddr}} <span class="muted">({{.Runtime.WebAddrSource}})</span></p>
<p>Kindle: {{.Runtime.KindleAddr}} <span class="muted">({{.Runtime.KindleSource}})</span></p>
{{if .Runtime.ConfigPath}}<p>Config: {{.Runtime.ConfigPath}}</p>{{end}}
</fieldset>
<form method="post" action="/settings">
<fieldset><legend>TXT conversion defaults</legend>
<label>Default author <input type="text" name="author" value="{{.Values.Author}}"></label>
<label>Default language <input type="text" name="language" value="{{.Values.Language}}" required></label>
<label><input type="checkbox" name="cover" value="1" {{if .Values.Cover}}checked{{end}}> Generate text cover</label>
<label>H1 regex <input type="text" name="h1_regex" value="{{.Values.TXT.H1Regex}}" required></label>
<label>H2 regex <input type="text" name="h2_regex" value="{{.Values.TXT.H2Regex}}" required></label>
<label>Split level <input type="number" name="split_level" min="1" max="2" value="{{.Values.TXT.SplitLevel}}" required></label>
<label><input type="checkbox" name="merge_lines" value="1" {{if .Values.TXT.MergeLines}}checked{{end}}> Merge wrapped lines</label>
<label><input type="checkbox" name="trim_blank_lines" value="1" {{if .Values.TXT.TrimBlankLines}}checked{{end}}> Compress consecutive blank lines</label>
<label>Drop regexes (one per line)<textarea name="drop_regex" rows="4">{{.DropRegex}}</textarea></label>
<label>Replacement rules (JSON array)<textarea name="replace_json" rows="4">{{.ReplaceJSON}}</textarea></label>
<label>Line height <input type="text" name="line_height" value="{{.Values.Style.LineHeight}}" required></label>
<label>Paragraph indent <input type="text" name="paragraph_indent" value="{{.Values.Style.ParagraphIndent}}" required></label>
<label>Paragraph spacing <input type="text" name="paragraph_spacing" value="{{.Values.Style.ParagraphSpacing}}" required></label>
<label>Text align <input type="text" name="text_align" value="{{.Values.Style.TextAlign}}" required></label>
</fieldset>
<fieldset><legend>Kindle page</legend><label><input type="checkbox" name="kindle_show_epub" value="1" {{if .Values.KindleShowEPUB}}checked{{end}}> Show latest EPUB alongside latest AZW3</label></fieldset>
<fieldset><legend>Proofreading defaults</legend>
<label>Codex model (blank uses Codex default) <input type="text" name="proofread_model" value="{{.Values.Proofread.Model}}"></label>
<label>Batch size (characters) <input type="number" name="proofread_batch_size" min="1000" max="50000" value="{{.Values.Proofread.BatchSize}}" required></label>
<label>Concurrency <input type="number" name="proofread_concurrency" min="1" max="8" value="{{.Values.Proofread.Concurrency}}" required></label>
</fieldset>
<button type="submit">Save global defaults</button></form>
<fieldset><legend>Library diagnostics</legend><p>Database schema: v{{.System.SchemaVersion}} · journal: {{.System.JournalMode}}</p><p>Library writable: {{if .System.LibraryWritable}}yes{{else}}no{{end}} · free space: {{.SystemFree}}</p><p>Generation slot: {{.System.RunningGeneration}} running, {{.System.QueuedGeneration}} queued</p><p>Proofreading slot: {{.System.RunningProofread}} running, {{.System.QueuedProofread}} queued</p><p class="muted">Both slots select tasks from the same persistent FIFO sequence.</p></fieldset>
<fieldset><legend>Dependency diagnostics</legend><p>python3: {{if .Diagnostics.PythonPath}}{{.Diagnostics.PythonPath}} — {{.Diagnostics.PythonVersion}}{{else}}not found{{end}}</p><p>Codex CLI: {{if .Diagnostics.CodexPath}}{{.Diagnostics.CodexPath}} — {{.Diagnostics.CodexVersion}}{{else}}not found{{end}}</p><p>Required non-interactive flags: {{if .Diagnostics.CodexFlagsReady}}available{{else}}missing or unsupported{{end}}</p><form method="post" action="/settings/check-codex"><button type="submit">Check Codex login</button></form><p class="muted">The explicit check runs <code>codex login status</code> and does not send book content or invoke a model.</p></fieldset>
</body></html>`))

var txtPreviewTemplate = template.Must(template.New("txt-preview").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>TXT Preview — Kindle Go</title></head><body>
<p><a href="/books/{{.BookID}}">← Back to book</a></p><h1>TXT Preview</h1>
<dl><dt>Charset</dt><dd>{{.Charset}}</dd><dt>Lines</dt><dd>original {{.OriginalLines}}, dropped {{.DroppedLines}}, blank {{.BlankLines}}, merged {{.MergedLines}}</dd><dt>Structure</dt><dd>{{.Sections}} sections, {{.Paragraphs}} paragraphs</dd></dl>
<h2>Table of contents</h2><ul>{{range .TOC}}<li>{{.}}</li>{{else}}<li>正文</li>{{end}}</ul>
</body></html>`))

var kindleTemplate = template.Must(template.New("kindle").Parse(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>Kindle Downloads</title>
</head>
<body>
  <h1>Downloads</h1>
  <p>{{if .ShowAll}}<a href="/">Recent</a>{{else}}<a href="/?all=1">All files</a>{{end}}</p>
  {{range .Records}}
    <h2>{{.OriginalName}}</h2>
    <p>{{.UploadedAt}}</p>
    <ul>
    {{range .Files}}
      <li><a href="{{.URL}}">{{.Name}}</a> ({{.Format}}, {{.Size}})</li>
    {{end}}
    </ul>
  {{else}}
    <p>No downloadable files.</p>
  {{end}}
</body>
</html>`))
