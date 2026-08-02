const activeTask = document.querySelector('[data-task-status="queued"], [data-task-status="running"]');
if (activeTask) {
  window.setTimeout(() => window.location.reload(), 2000);
}
