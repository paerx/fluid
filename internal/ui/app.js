const state = {
  token: localStorage.getItem("fluid-token") || "",
  currentPage: "overview",
  overview: null,
  metrics: null,
  cpu: null,
  goroutineSeries: [],
  topGroups: [],
  activeGroupId: null,
  groupDetail: null,
  eventSource: null,
  pipelineTick: 0,
  pipelineStarted: false,
};

const basePath = window.location.pathname.replace(/\/$/, "");

const elements = {
  authFlow: document.getElementById("authFlow"),
  authCard: document.getElementById("authCard"),
  tokenInput: document.getElementById("tokenInput"),
  tokenSubmit: document.getElementById("tokenSubmit"),
  authError: document.getElementById("authError"),
  faceImage: document.getElementById("faceImage"),
  faceHint: document.getElementById("faceHint"),
  dashboard: document.getElementById("dashboard"),
  navLinks: [...document.querySelectorAll(".nav-link")],
  groupList: document.getElementById("groupList"),
  stackDetail: document.getElementById("stackDetail"),
  cpuGrid: document.getElementById("cpuGrid"),
  cpuText: document.getElementById("cpuText"),
  pipelineCanvas: document.getElementById("pipelineCanvas"),
  goroutinesChart: document.getElementById("goroutinesChart"),
};

const pageIds = ["overview", "goroutines"];
elements.tokenInput.value = state.token;

bindEvents();
bootstrap();
animateBackground();

function bootstrap() {
  const params = new URLSearchParams(window.location.search);
  if (params.has("token") && !state.token) {
    state.token = params.get("token");
    localStorage.setItem("fluid-token", state.token);
    elements.tokenInput.value = state.token;
  }
  elements.authFlow.classList.add("visible");
  elements.dashboard.classList.add("hidden");
}

function bindEvents() {
  elements.tokenSubmit.addEventListener("click", async () => {
    state.token = elements.tokenInput.value.trim();
    if (state.token) {
      localStorage.setItem("fluid-token", state.token);
    } else {
      localStorage.removeItem("fluid-token");
    }

    const open = await canOpenDashboard();
    if (!open) {
      elements.authError.textContent = "Token rejected by the SDK.";
      await playFaceFail();
      return;
    }
    await playFaceSuccess();
    await enterDashboard();
  });

  elements.navLinks.forEach((button) => {
    button.addEventListener("click", () => setPage(button.dataset.page));
  });

  window.addEventListener("resize", () => renderCharts());
  window.addEventListener("pointermove", (event) => {
    const x = (event.clientX / window.innerWidth) * 100;
    const y = (event.clientY / window.innerHeight) * 100;
    document.documentElement.style.setProperty("--mx", `${x}%`);
    document.documentElement.style.setProperty("--my", `${y}%`);
  });
}

async function canOpenDashboard() {
  try {
    const res = await apiFetch("/api/v1/health");
    return res.ok;
  } catch {
    return false;
  }
}

async function enterDashboard() {
  elements.authError.textContent = "";
  elements.authFlow.classList.remove("visible");
  elements.authFlow.classList.add("hidden");
  elements.authCard.classList.remove("scanning", "auth-card-scan");
  elements.dashboard.classList.remove("hidden");
  await refreshAll();
  startPipelineLoop();
  openStream();
}

async function refreshAll() {
  const [overview, cpu, gSeries] = await Promise.all([
    apiJson("/api/v1/overview"),
    apiJson("/api/v1/cpu"),
    apiJson("/api/v1/timeseries?metric=goroutines&window=15m&step=1s"),
  ]);

  state.overview = overview;
  state.cpu = cpu;
  state.goroutineSeries = gSeries.points || [];
  await triggerSnapshot();
  renderAll();
}

async function triggerSnapshot() {
  try {
    await apiJson("/api/v1/snapshot/goroutines", {
      method: "POST",
      body: JSON.stringify({
        groupBy: "topFunction",
        topN: 20,
        withDelta: true,
        exclude: ["runtime.", "net/http"],
      }),
    });
  } catch {
    // Snapshot throttling is not fatal for the UI.
  }
  const top = await apiJson("/api/v1/goroutines/top");
  state.topGroups = top.items || [];
  if (!state.activeGroupId && state.topGroups[0]) {
    state.activeGroupId = state.topGroups[0].id;
  }
  if (state.activeGroupId) {
    await loadGroup(state.activeGroupId);
  }
}

async function loadGroup(id) {
  state.activeGroupId = id;
  state.groupDetail = await apiJson(`/api/v1/goroutines/group?id=${encodeURIComponent(id)}`);
  renderGroups();
  renderGroupDetail();
}

function openStream() {
  if (state.eventSource) {
    state.eventSource.close();
  }
  const token = state.token ? `&token=${encodeURIComponent(state.token)}` : "";
  state.eventSource = new EventSource(`${basePath}/api/v1/stream?topics=overview,metrics,cpu,snapshot${token}`);

  state.eventSource.addEventListener("overview", (event) => {
    state.overview = JSON.parse(event.data);
    renderOverview();
  });

  state.eventSource.addEventListener("metrics", (event) => {
    state.metrics = JSON.parse(event.data);
    state.goroutineSeries.push({ ts: state.metrics.ts, v: state.metrics.goroutines });
    if (state.goroutineSeries.length > 900) {
      state.goroutineSeries.shift();
    }
    renderLiveMetrics();
    renderCharts();
  });

  state.eventSource.addEventListener("cpu", (event) => {
    state.cpu = JSON.parse(event.data);
    renderCPU();
  });

  state.eventSource.addEventListener("snapshot", async (event) => {
    const payload = JSON.parse(event.data);
    state.topGroups = payload.items || [];
    if (!state.activeGroupId && state.topGroups[0]) {
      state.activeGroupId = state.topGroups[0].id;
    }
    if (state.activeGroupId) {
      await loadGroup(state.activeGroupId);
    }
    renderGroups();
  });
}

function renderAll() {
  renderOverview();
  renderLiveMetrics();
  renderCPU();
  renderGroups();
  renderGroupDetail();
  renderCharts();
  renderPipeline();
}

function renderOverview() {
  if (!state.overview) return;
  const { kpi, serverTime } = state.overview;
  setText("overviewGoroutines", formatNumber(kpi.goroutines.value));
  setText("overviewGTrend", formatTrend(kpi.goroutines.trendPerMin, "/min"));
  setText("overviewHeap", kpi.heapInuse.display || humanBytes(kpi.heapInuse.valueBytes));
  setText("overviewHeapTrend", formatByteTrend(kpi.heapInuse.trendBytesPerMin));
  setText("overviewGC", `${(kpi.gcP95.valueMs || 0).toFixed(1)} ms`);
  setText("overviewGCStatus", kpi.gcP95.status || "stable");
  setText("overviewCPU", kpi.cpu.display || `${Math.round(kpi.cpu.valuePct || 0)}%`);
  setText("overviewCPUTrend", `avg ${Math.round(kpi.cpu.trendPct || 0)}%`);
  setText("overviewTime", serverTime.time);
  setText("overviewDate", serverTime.date);
}

function renderLiveMetrics() {
  const latestPoint = state.goroutineSeries[state.goroutineSeries.length - 1];
  const previous = state.goroutineSeries[Math.max(0, state.goroutineSeries.length - 61)];
  const trend = latestPoint && previous ? latestPoint.v - previous.v : 0;
  setText("goroutinesKpi", latestPoint ? formatNumber(latestPoint.v) : "0");
  setText("goroutinesTrend", `${trend >= 0 ? "+" : ""}${Math.round(trend)}/min`);
  renderPipeline();
}

function renderCPU() {
  if (!state.cpu) return;
  elements.cpuGrid.innerHTML = "";
  (state.cpu.grid?.cells || []).forEach((cell) => {
    const div = document.createElement("div");
    div.className = `cpu-cell ${String(cell.state || "COOL").toLowerCase()}`;
    elements.cpuGrid.appendChild(div);
  });
  elements.cpuText.textContent = state.cpu.text || "0 / 0";
}

function renderGroups() {
  elements.groupList.innerHTML = "";
  state.topGroups.forEach((item) => {
    const row = document.createElement("div");
    row.className = `group-item ${item.id === state.activeGroupId ? "active" : ""}`;
    row.innerHTML = `
      <span>${item.name}</span>
      <span>${formatNumber(item.count)}</span>
      <span>${item.id === state.activeGroupId ? '<i class="group-arrow"></i>' : ""}</span>
    `;
    row.addEventListener("click", () => loadGroup(item.id));
    elements.groupList.appendChild(row);
  });
}

function renderGroupDetail() {
  const detail = state.groupDetail;
  if (!detail) {
    elements.stackDetail.innerHTML = "<div class='detail-block'>No snapshot captured.</div>";
    return;
  }
  const frames = (detail.topFrames || [])
    .map((frame, index) => `<div class="frame-line">${index + 1}) ${frame}</div>`)
    .join("");
  elements.stackDetail.innerHTML = `
    <div class="detail-block">
      <span class="detail-label">${detail.name}</span>
      <div>State: ${detail.stateTop}</div>
    </div>
    <div class="detail-block">
      <span class="detail-label">Top frames:</span>
      ${frames}
    </div>
  `;
}

function renderCharts() {
  renderLineChart(elements.goroutinesChart, state.goroutineSeries, { baselineLabel: "goroutines" });
}

function renderPipeline() {
  const canvas = elements.pipelineCanvas;
  if (!canvas || state.currentPage !== "goroutines") {
    return;
  }
  const ctx = canvas.getContext("2d");
  const ratio = window.devicePixelRatio || 1;
  const widthAttr = Number(canvas.dataset.baseWidth || canvas.getAttribute("width") || 1100);
  const heightAttr = Number(canvas.dataset.baseHeight || canvas.getAttribute("height") || 180);
  const rect = canvas.getBoundingClientRect();
  const cssWidth = rect.width || canvas.parentElement?.clientWidth || widthAttr;
  const cssHeight = rect.height || (cssWidth * heightAttr) / widthAttr;
  const width = Math.max(1, Math.round(cssWidth * ratio));
  const height = Math.max(1, Math.round(cssHeight * ratio));
  canvas.width = width;
  canvas.height = height;
  ctx.clearRect(0, 0, width, height);

  const latest = state.goroutineSeries[state.goroutineSeries.length - 1];
  const baseValue = latest ? Math.max(1, Math.round(latest.v)) : 48;
  const count = 30;
  const paddingX = 34 * ratio;
  const paddingY = 20 * ratio;
  const centerY = height * 0.54;
  const trackLeft = paddingX;
  const trackRight = width - paddingX;
  const visibleWidth = trackRight - trackLeft;
  const stepX = visibleWidth / 7.6;
  const totalSpan = stepX * count;
  const flowOffset = (state.pipelineTick * 1.2 * ratio) % stepX;
  const rows = [
    centerY - 42 * ratio,
    centerY - 2 * ratio,
    centerY + 38 * ratio,
  ];

  const gradient = ctx.createLinearGradient(0, 0, width, height);
  gradient.addColorStop(0, "rgba(255,255,255,0.05)");
  gradient.addColorStop(1, "rgba(255,255,255,0.01)");
  ctx.fillStyle = gradient;
  roundRect(ctx, paddingX * 0.12, paddingY * 0.4, width - paddingX * 0.24, height - paddingY * 0.8, 28 * ratio);
  ctx.fill();
  ctx.strokeStyle = "rgba(255,255,255,0.14)";
  ctx.stroke();

  state.pipelineTick += 1;
  rows.forEach((rowY, laneIndex) => {
    const lane = ctx.createLinearGradient(trackLeft, rowY, trackRight, rowY);
    lane.addColorStop(0, "rgba(255,255,255,0)");
    lane.addColorStop(0.12, "rgba(255,255,255,0.05)");
    lane.addColorStop(0.5, laneIndex === 1 ? "rgba(150,220,255,0.09)" : "rgba(255,255,255,0.04)");
    lane.addColorStop(0.88, "rgba(255,255,255,0.05)");
    lane.addColorStop(1, "rgba(255,255,255,0)");
    ctx.strokeStyle = lane;
    ctx.lineWidth = (laneIndex === 1 ? 11 : 7) * ratio;
    ctx.lineCap = "round";
    ctx.beginPath();
    ctx.moveTo(trackLeft, rowY);
    ctx.bezierCurveTo(width * 0.28, rowY - 4 * ratio, width * 0.64, rowY + 5 * ratio, trackRight, rowY);
    ctx.stroke();
  });

  for (let index = 0; index < count; index += 1) {
    const lane = index % rows.length;
    const sequenceValue = Math.max(1, baseValue - index);
    const baseX = trackRight - index * stepX - flowOffset - lane * stepX * 0.12;
    const wrappedX = ((baseX - trackLeft + totalSpan) % totalSpan) + trackLeft;
    if (wrappedX < trackLeft - 40 * ratio || wrappedX > trackRight + 22 * ratio) {
      continue;
    }

    const y = rows[lane] + Math.sin((wrappedX / visibleWidth) * Math.PI * 1.15 + lane * 0.7) * (lane === 1 ? 1.8 : 2.8) * ratio;
    const isMarker = sequenceValue % 10 === 0;
    const size = (lane === 1 ? 16 : 14) * ratio;
    const edgeFade = Math.min(1, Math.max(0, (wrappedX - trackLeft) / (visibleWidth * 0.14)), Math.max(0, (trackRight - wrappedX) / (visibleWidth * 0.12)));
    const alpha = 0.52 + edgeFade * 0.34;

    const tail = ctx.createLinearGradient(wrappedX + size * 0.8, y, wrappedX - size * 2.9, y);
    if (isMarker) {
      tail.addColorStop(0, "rgba(60,255,52,0.16)");
      tail.addColorStop(0.45, "rgba(60,255,52,0.08)");
      tail.addColorStop(1, "rgba(60,255,52,0)");
    } else {
      tail.addColorStop(0, `rgba(255,255,255,${alpha * 0.16})`);
      tail.addColorStop(0.45, `rgba(180,220,255,${alpha * 0.08})`);
      tail.addColorStop(1, "rgba(255,255,255,0)");
    }
    ctx.strokeStyle = tail;
    ctx.lineWidth = 5.5 * ratio;
    ctx.lineCap = "round";
    ctx.beginPath();
    ctx.moveTo(wrappedX + size * 0.45, y);
    ctx.lineTo(wrappedX - size * 2.2, y);
    ctx.stroke();

    let greenBoost = 0;
    if (isMarker) {
      const pulse = ((state.pipelineTick * 0.018) + index * 0.13) % 1;
      const flashA = Math.exp(-Math.pow((pulse - 0.18) / 0.05, 2));
      const flashB = Math.exp(-Math.pow((pulse - 0.34) / 0.05, 2));
      greenBoost = Math.min(0.42, (flashA + flashB) * 0.22);
      ctx.shadowBlur = (14 + greenBoost * 26) * ratio;
      ctx.shadowColor = "rgba(60,255,52,0.68)";
      ctx.fillStyle = `rgba(60,255,52,${0.78 + greenBoost})`;
    } else {
      ctx.shadowBlur = 5 * ratio;
      ctx.shadowColor = "rgba(255,255,255,0.18)";
      ctx.fillStyle = `rgba(255,255,255,${alpha})`;
    }
    roundRect(ctx, wrappedX - size * 0.5, y - size * 0.5, size, size, 4.5 * ratio);
    ctx.fill();
    ctx.shadowBlur = 0;

    ctx.fillStyle = isMarker ? `rgba(60,255,52,${0.9 + greenBoost * 0.2})` : "rgba(255,255,255,0.54)";
    ctx.font = `${10 * ratio}px "SF Pro Display", sans-serif`;
    ctx.textAlign = "center";
    ctx.fillText(String(sequenceValue), wrappedX, y - 14 * ratio);
  }
  ctx.textAlign = "start";

}

function startPipelineLoop() {
  if (state.pipelineStarted) return;
  state.pipelineStarted = true;
  const loop = () => {
    renderPipeline();
    requestAnimationFrame(loop);
  };
  requestAnimationFrame(loop);
}

function renderLineChart(canvas, points, options = {}) {
  if (!canvas) return;
  const ctx = canvas.getContext("2d");
  const ratio = window.devicePixelRatio || 1;
  const widthAttr = Number(canvas.dataset.baseWidth || canvas.getAttribute("width") || 1400);
  const heightAttr = Number(canvas.dataset.baseHeight || canvas.getAttribute("height") || 520);
  const rect = canvas.getBoundingClientRect();
  const cssWidth = rect.width || canvas.parentElement?.clientWidth || widthAttr;
  const cssHeight = rect.height || (cssWidth * heightAttr) / widthAttr;
  const width = Math.max(1, Math.round(cssWidth * ratio));
  const height = Math.max(1, Math.round(cssHeight * ratio));
  canvas.width = width;
  canvas.height = height;
  ctx.clearRect(0, 0, width, height);

  const safeData = points
    .slice(-180)
    .map((point) => Number(point?.v))
    .filter((value) => Number.isFinite(value));
  const data = safeData.length
    ? safeData
    : [12, 36, 28, 44, 16, 62, 48, 41, 58];
  const min = Math.min(...data);
  const max = Math.max(...data);
  const range = max - min || 1;
  const padding = 40 * ratio;

  ctx.strokeStyle = "rgba(255,255,255,0.18)";
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(padding, padding);
  ctx.lineTo(padding, height - padding);
  ctx.lineTo(width - padding, height - padding);
  ctx.stroke();

  ctx.beginPath();
  data.forEach((value, index) => {
    const x = padding + ((width - padding * 2) * index) / (data.length - 1 || 1);
    const y = height - padding - ((value - min) / range) * (height - padding * 2);
    if (index === 0) ctx.moveTo(x, y);
    else ctx.lineTo(x, y);
  });
  const stroke = options.stroke || "rgba(245,247,251,0.92)";
  ctx.strokeStyle = stroke;
  ctx.lineWidth = 2;
  ctx.shadowBlur = 28 * ratio;
  ctx.shadowColor = options.glow || "rgba(255,255,255,0.08)";
  ctx.stroke();

  const fill = ctx.createLinearGradient(0, padding, 0, height - padding);
  fill.addColorStop(0, "rgba(255,255,255,0.14)");
  fill.addColorStop(1, "rgba(255,255,255,0)");
  ctx.lineTo(width - padding, height - padding);
  ctx.lineTo(padding, height - padding);
  ctx.closePath();
  ctx.fillStyle = fill;
  ctx.fill();

  ctx.beginPath();
  data.forEach((value, index) => {
    const x = padding + ((width - padding * 2) * index) / (data.length - 1 || 1);
    const y = height - padding - ((value - min) / range) * (height - padding * 2);
    if (index === 0) ctx.moveTo(x, y);
    else ctx.lineTo(x, y);
  });
  ctx.strokeStyle = stroke;
  ctx.lineWidth = 2.2 * ratio;
  ctx.shadowBlur = 34 * ratio;
  ctx.shadowColor = options.glow || "rgba(255,255,255,0.12)";
  ctx.stroke();
  ctx.shadowBlur = 0;
}

function setPage(page) {
  state.currentPage = page;
  pageIds.forEach((id) => {
    document.getElementById(id).classList.toggle("active", id === page);
  });
  elements.navLinks.forEach((button) => {
    button.classList.toggle("active", button.dataset.page === page);
  });
  renderCharts();
  renderPipeline();
}

async function playFaceSuccess() {
  elements.authCard.classList.add("scanning", "auth-card-scan");
  elements.faceImage.src = "./assets/face-id.png";
  elements.faceHint.textContent = "Face ID handshake in progress";
  await wait(900);
  elements.faceImage.src = "./assets/face-success.png";
  elements.faceHint.textContent = "Identity accepted";
  await wait(650);
}

async function playFaceFail() {
  elements.authCard.classList.add("scanning", "auth-card-scan");
  elements.faceImage.src = "./assets/face-failed.png";
  elements.faceHint.textContent = "Identity mismatch";
  await wait(900);
  elements.authCard.classList.remove("scanning", "auth-card-scan");
  elements.faceImage.src = "./assets/face-id.png";
  elements.faceHint.textContent = "Face ID handshake in progress";
}

function animateBackground() {
  let tick = 0;
  const step = () => {
    tick += 0.01;
    document.documentElement.style.setProperty("--pulse", `${1 + Math.sin(tick) * 0.12}`);
    requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
}

async function apiJson(path, options = {}) {
  const res = await apiFetch(path, options);
  if (!res.ok) {
    throw new Error(`Request failed: ${res.status}`);
  }
  return res.json();
}

function apiFetch(path, options = {}) {
  const headers = new Headers(options.headers || {});
  if (state.token) {
    headers.set("Authorization", `Bearer ${state.token}`);
    headers.set("X-Fluid-Token", state.token);
  }
  return fetch(`${basePath}${path}`, { ...options, headers });
}

function setText(id, value) {
  const node = document.getElementById(id);
  if (node) {
    node.textContent = value;
  }
}

function formatNumber(value) {
  return new Intl.NumberFormat().format(Math.round(Number(value) || 0));
}

function humanBytes(value) {
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let current = Number(value);
  let unit = 0;
  while (current >= 1024 && unit < units.length - 1) {
    current /= 1024;
    unit += 1;
  }
  return `${current.toFixed(current >= 100 ? 0 : 1)} ${units[unit]}`;
}

function formatTrend(value, suffix) {
  return `${value >= 0 ? "+" : ""}${Math.round(value || 0)}${suffix}`;
}

function formatByteTrend(value) {
  const sign = value >= 0 ? "+" : "-";
  return `${sign}${humanBytes(Math.abs(value || 0))}/min`;
}

function wait(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function roundRect(ctx, x, y, width, height, radius) {
  ctx.beginPath();
  ctx.moveTo(x + radius, y);
  ctx.arcTo(x + width, y, x + width, y + height, radius);
  ctx.arcTo(x + width, y + height, x, y + height, radius);
  ctx.arcTo(x, y + height, x, y, radius);
  ctx.arcTo(x, y, x + width, y, radius);
  ctx.closePath();
}
