let currentRange = '1h';
let currentTab = 'overview';
let charts = {};
let detailCharts = {};
let refreshInterval = null;

const C = {
    upload: '#3fb950', download: '#58a6ff',
    rttAvg: '#d2a8ff', rttP95: '#f0883e',
    jitter: '#d29922', loss: '#f85149',
    flows: '#79c0ff', dns: '#a5d6ff',
    quality: '#3fb950', qualityBg: '#3fb95020',
};

// --- Threshold line helpers ---
function thresholdLine(value, color, label) {
    return {
        type: 'line',
        yMin: value, yMax: value,
        borderColor: color,
        borderWidth: 1,
        borderDash: [6, 4],
        label: {
            display: true,
            content: label,
            position: 'start',
            color: color,
            backgroundColor: 'transparent',
            font: { size: 9, weight: 'normal' },
            padding: { top: 0, bottom: 0, left: 2, right: 2 }
        }
    };
}

function thresholdBand(yMin, yMax, color) {
    return {
        type: 'box',
        yMin: yMin, yMax: yMax,
        backgroundColor: color,
        borderWidth: 0,
    };
}

const annotations = {
    quality: {
        bandGood:     thresholdBand(80, 100, '#3fb95010'),
        bandDegraded: thresholdBand(50, 80, '#d2992210'),
        bandBad:      thresholdBand(0, 50, '#f8514910'),
        lineGood:     thresholdLine(80, '#3fb95060', 'Good'),
        lineDegraded: thresholdLine(50, '#d2992260', 'Degraded'),
    },
    latency: {
        lineGreat:    thresholdLine(30, '#3fb95050', '30ms Great'),
        lineDegraded: thresholdLine(100, '#d2992250', '100ms OK'),
        lineBad:      thresholdLine(200, '#f8514950', '200ms Bad'),
    },
    jitter: {
        lineGreat:    thresholdLine(10, '#3fb95050', '10ms Great'),
        lineDegraded: thresholdLine(30, '#d2992250', '30ms OK'),
        lineBad:      thresholdLine(50, '#f8514950', '50ms Bad'),
    },
    loss: {
        lineGreat:    thresholdLine(0.1, '#3fb95050', '0.1% Great'),
        lineDegraded: thresholdLine(1.0, '#d2992250', '1% OK'),
        lineBad:      thresholdLine(5.0, '#f8514950', '5% Bad'),
    },
    dns: {
        lineGreat:    thresholdLine(50, '#3fb95050', '50ms Fast'),
        lineDegraded: thresholdLine(100, '#d2992250', '100ms Slow'),
    },
    download: {
        lineHD:       thresholdLine(3 * 1024 * 1024, '#58a6ff30', '3 MB/s HD Video'),
        line4K:       thresholdLine(25 * 1024 * 1024, '#d2a8ff30', '25 MB/s 4K'),
    },
    upload: {
        lineVideo:    thresholdLine(3 * 1024 * 1024, '#3fb95030', '3 MB/s Video Call'),
        lineStream:   thresholdLine(5 * 1024 * 1024, '#d2a8ff30', '5 MB/s Game Stream'),
    },
};

// --- Base options ---
const baseOpts = {
    responsive: true, maintainAspectRatio: false,
    animation: { duration: 300 },
    plugins: {
        legend: { labels: { color: '#8b949e', boxWidth: 12, padding: 8, font: { size: 11 } } },
        tooltip: { mode: 'index', intersect: false },
        annotation: { annotations: {} }
    },
    scales: {
        x: { type: 'time', grid: { color: '#21262d' }, ticks: { color: '#8b949e', maxTicksLimit: 8, font: { size: 10 } } },
        y: { beginAtZero: true, grid: { color: '#21262d' }, ticks: { color: '#8b949e', font: { size: 10 } } }
    },
    elements: { point: { radius: 0 }, line: { borderWidth: 1.5, tension: 0.3, spanGaps: false } }
};

function makeOpts(yCallback, annots, tooltipOpts) {
    return {
        ...baseOpts,
        plugins: {
            ...baseOpts.plugins,
            annotation: { annotations: annots || {} },
            tooltip: { mode: 'index', intersect: false, ...(tooltipOpts || {}) }
        },
        scales: { ...baseOpts.scales, y: { ...baseOpts.scales.y, ticks: { ...baseOpts.scales.y.ticks, callback: yCallback } } }
    };
}

// --- Quality band helpers for tooltips ---
function qualityBand(score) {
    if (score == null) return '';
    if (score >= 80) return ' (Good)';
    if (score >= 50) return ' (Degraded)';
    return ' (Bad)';
}

function ratingInverse(val, great, ok, bad) {
    if (val == null) return '';
    if (val <= great) return ' (Great)';
    if (val <= ok) return ' (OK)';
    return ' (Bad)';
}

function ratingDirect(val, great, ok, bad) {
    if (val == null) return '';
    if (val >= great) return ' (Great)';
    if (val >= ok) return ' (OK)';
    return ' (Bad)';
}

function tooltipWithBand(ratingFn) {
    return {
        callbacks: {
            label: function(ctx) {
                let label = ctx.dataset.label || '';
                if (label) label += ': ';
                const val = ctx.parsed.y;
                if (val == null) return label + 'No data';
                label += ctx.formattedValue;
                label += ratingFn(val);
                return label;
            }
        }
    };
}

function initCharts() {
    charts.quality = new Chart(document.getElementById('chart-quality'), {
        type: 'line',
        data: { datasets: [{ label: 'Quality', borderColor: C.quality, backgroundColor: C.qualityBg, fill: true, data: [] }] },
        options: {
            ...baseOpts,
            plugins: {
                ...baseOpts.plugins,
                annotation: { annotations: annotations.quality },
                tooltip: { mode: 'index', intersect: false, ...tooltipWithBand(v => qualityBand(v)) }
            },
            scales: { ...baseOpts.scales, y: { ...baseOpts.scales.y, min: 0, max: 100, ticks: { ...baseOpts.scales.y.ticks, callback: v => v } } }
        }
    });

    charts.download = new Chart(document.getElementById('chart-download'), {
        type: 'line',
        data: { datasets: [
            { label: 'TCP', borderColor: C.download, data: [] },
            { label: 'UDP', borderColor: '#388bfd66', borderDash: [4,4], data: [] }
        ] },
        options: makeOpts(v => fmtBytes(v) + '/s', annotations.download,
            tooltipWithBand(v => ratingDirect(v, 25*1024*1024, 5*1024*1024, 1*1024*1024)))
    });

    charts.upload = new Chart(document.getElementById('chart-upload'), {
        type: 'line',
        data: { datasets: [
            { label: 'TCP', borderColor: C.upload, data: [] },
            { label: 'UDP', borderColor: '#2ea04366', borderDash: [4,4], data: [] }
        ] },
        options: makeOpts(v => fmtBytes(v) + '/s', annotations.upload,
            tooltipWithBand(v => ratingDirect(v, 10*1024*1024, 2*1024*1024, 512*1024)))
    });

    charts.latency = new Chart(document.getElementById('chart-latency'), {
        type: 'line',
        data: { datasets: [
            { label: 'Avg', borderColor: C.rttAvg, data: [] },
            { label: 'P95', borderColor: C.rttP95, data: [] }
        ] },
        options: makeOpts(v => v.toFixed(0) + 'ms', annotations.latency,
            tooltipWithBand(v => ratingInverse(v, 30, 100, 200)))
    });

    charts.jitter = new Chart(document.getElementById('chart-jitter'), {
        type: 'line',
        data: { datasets: [{ label: 'Jitter', borderColor: C.jitter, backgroundColor: C.jitter + '20', fill: true, data: [] }] },
        options: makeOpts(v => v.toFixed(1) + 'ms', annotations.jitter,
            tooltipWithBand(v => ratingInverse(v, 10, 30, 50)))
    });

    charts.loss = new Chart(document.getElementById('chart-loss'), {
        type: 'line',
        data: { datasets: [{ label: 'Packet Loss', borderColor: C.loss, backgroundColor: C.loss + '20', fill: true, data: [] }] },
        options: makeOpts(v => v.toFixed(1) + '%', annotations.loss,
            tooltipWithBand(v => ratingInverse(v, 0.1, 1.0, 5.0)))
    });

    charts.dns = new Chart(document.getElementById('chart-dns'), {
        type: 'line',
        data: { datasets: [{ label: 'DNS', borderColor: C.dns, backgroundColor: C.dns + '20', fill: true, data: [] }] },
        options: makeOpts(v => v.toFixed(0) + 'ms', annotations.dns,
            tooltipWithBand(v => ratingInverse(v, 50, 100, 200)))
    });
}

// --- Fetching ---

async function fetchMetrics() {
    try {
        const resp = await fetch(`/api/metrics?range=${currentRange}`);
        const json = await resp.json();
        updateOverview(json.data);
        updateUseCases(json.data);
    } catch (e) { console.error('metrics fetch failed:', e); }
}

async function fetchStatus() {
    try {
        const resp = await fetch('/api/status');
        const json = await resp.json();
        document.getElementById('iface').textContent = json.interface;
        document.getElementById('flows').textContent = json.active_flows;
        document.getElementById('uptime').textContent = json.uptime;
    } catch (e) { console.error('status fetch failed:', e); }
}

async function fetchBreakdown(kind) {
    try {
        const resp = await fetch(`/api/breakdown?kind=${kind}&range=${currentRange}`);
        const json = await resp.json();
        updateBreakdownTable(kind, json.data);
    } catch (e) { console.error(`breakdown ${kind} fetch failed:`, e); }
}

async function fetchBreakdownTS(kind, key) {
    try {
        const resp = await fetch(`/api/breakdown/ts?kind=${kind}&key=${encodeURIComponent(key)}&range=${currentRange}`);
        const json = await resp.json();
        updateDetailChart(kind, key, json.data);
    } catch (e) { console.error(`breakdown ts fetch failed:`, e); }
}

// --- Update overview ---

function updateOverview(data) {
    if (!data || data.length === 0) return;
    const ts = data.map(d => new Date(d.ts * 1000));

    // Quality: null when no score available
    charts.quality.data.datasets[0].data = data.map((d, i) => ({
        x: ts[i], y: d.quality_avg != null && d.quality_avg >= 0 ? d.quality_avg : null
    }));
    charts.quality.update();

    // Throughput: 0 is valid (no traffic = 0 bytes), so keep as-is
    const tcpDown = data.map(d => (d.download_avg || 0) - (d.udp_download_avg || 0));
    charts.download.data.datasets[0].data = data.map((d, i) => ({ x: ts[i], y: Math.max(0, tcpDown[i]) }));
    charts.download.data.datasets[1].data = data.map((d, i) => ({ x: ts[i], y: d.udp_download_avg || 0 }));
    charts.download.update();

    const tcpUp = data.map(d => (d.upload_avg || 0) - (d.udp_upload_avg || 0));
    charts.upload.data.datasets[0].data = data.map((d, i) => ({ x: ts[i], y: Math.max(0, tcpUp[i]) }));
    charts.upload.data.datasets[1].data = data.map((d, i) => ({ x: ts[i], y: d.udp_upload_avg || 0 }));
    charts.upload.update();

    // Latency/jitter/loss/DNS: null when no data (not 0 — 0 would mean "perfect")
    charts.latency.data.datasets[0].data = data.map((d, i) => ({ x: ts[i], y: d.rtt_avg != null ? d.rtt_avg : null }));
    charts.latency.data.datasets[1].data = data.map((d, i) => ({ x: ts[i], y: d.rtt_p95 != null ? d.rtt_p95 : null }));
    charts.latency.update();

    charts.jitter.data.datasets[0].data = data.map((d, i) => ({ x: ts[i], y: d.jitter_avg != null ? d.jitter_avg : null }));
    charts.jitter.update();

    charts.loss.data.datasets[0].data = data.map((d, i) => ({ x: ts[i], y: d.loss_avg != null ? d.loss_avg : null }));
    charts.loss.update();

    charts.dns.data.datasets[0].data = data.map((d, i) => ({ x: ts[i], y: d.dns_avg != null ? d.dns_avg : null }));
    charts.dns.update();

    // Current values
    const L = data[data.length - 1];
    document.getElementById('download-value').innerHTML = fmtSpeed(L.download_avg || 0);
    document.getElementById('upload-value').innerHTML = fmtSpeed(L.upload_avg || 0);
    document.getElementById('latency-value').innerHTML = (L.rtt_avg ? L.rtt_avg.toFixed(1) : '--') + ' <span class="unit">ms</span>';
    document.getElementById('jitter-value').innerHTML = (L.jitter_avg ? L.jitter_avg.toFixed(1) : '--') + ' <span class="unit">ms</span>';
    document.getElementById('loss-value').innerHTML = (L.loss_avg != null ? L.loss_avg.toFixed(2) : '--') + ' <span class="unit">%</span>';
    document.getElementById('dns-value').innerHTML = (L.dns_avg ? L.dns_avg.toFixed(0) : '--') + ' <span class="unit">ms</span>';

    updateQualityDisplay(L.quality_avg);
}

function updateQualityDisplay(score) {
    const box = document.getElementById('quality-box');
    const num = document.getElementById('quality-number');
    box.classList.remove('good', 'degraded', 'bad');
    if (score == null || score < 0) { num.textContent = '--'; return; }
    num.textContent = Math.round(score);
    if (score >= 80) box.classList.add('good');
    else if (score >= 50) box.classList.add('degraded');
    else box.classList.add('bad');
}

function updateUseCases(data) {
    if (!data || data.length === 0) return;
    const L = data[data.length - 1];
    const down = L.download_avg || 0, up = L.upload_avg || 0;
    const rtt = L.rtt_avg, jitter = L.jitter_avg, loss = L.loss_avg;

    // If idle (no meaningful traffic and no quality data), show neutral state
    const idle = down < 1000 && up < 1000 && rtt == null;
    if (idle) {
        ['hd_video_call', 'audio_call', 'screen_sharing', 'game_streaming', '4k_streaming'].forEach(id => {
            const el = document.getElementById('uc-' + id);
            if (el) el.classList.remove('good', 'degraded', 'bad');
        });
        return;
    }

    setUC('hd_video_call', down, up, rtt, jitter, loss, 3*1024*1024, 3*1024*1024, 100, 20, 0.5);
    setUC('audio_call', down, up, rtt, jitter, loss, 100*1024, 100*1024, 150, 30, 1.0);
    setUC('screen_sharing', down, up, rtt, jitter, loss, 1*1024*1024, 2*1024*1024, 200, 50, 2.0);
    setUC('game_streaming', down, up, rtt, jitter, loss, 5*1024*1024, 5*1024*1024, 50, 15, 1.0);
    setUC('4k_streaming', down, up, rtt, jitter, loss, 25*1024*1024, 0, 500, 100, 5.0);
}

function setUC(id, down, up, rtt, jitter, loss, needDown, needUp, maxRTT, maxJitter, maxLoss) {
    const el = document.getElementById('uc-' + id);
    if (!el) return;
    el.classList.remove('good', 'degraded', 'bad');
    let good = true, degraded = false;
    // Throughput checks (0 is a real value — means no traffic right now)
    if (needDown > 0 && down < needDown) { down >= needDown * 0.5 ? degraded = true : good = false; }
    if (needUp > 0 && up < needUp) { up >= needUp * 0.5 ? degraded = true : good = false; }
    // Quality checks — only evaluate when we have data (not null)
    if (rtt != null && rtt > maxRTT) { rtt <= maxRTT * 1.5 ? degraded = true : good = false; }
    if (jitter != null && jitter > maxJitter) { jitter <= maxJitter * 2 ? degraded = true : good = false; }
    if (loss != null && loss > maxLoss) { loss <= maxLoss * 3 ? degraded = true : good = false; }
    if (!good) el.classList.add('bad');
    else if (degraded) el.classList.add('degraded');
    else el.classList.add('good');
}

// --- Breakdown tables ---

function updateBreakdownTable(kind, data) {
    const tableId = kind === 'app' ? 'apps-table' : 'dest-table';
    const tbody = document.querySelector(`#${tableId} tbody`);
    tbody.innerHTML = '';
    if (!data || data.length === 0) {
        tbody.innerHTML = '<tr><td colspan="5" style="text-align:center;color:#8b949e">No data</td></tr>';
        return;
    }
    data.forEach(row => {
        const tr = document.createElement('tr');
        const label = kind === 'dest' && row.hostname && row.hostname !== row.key
            ? `${row.hostname}<span class="hostname">${row.key}</span>` : row.key || 'unknown';
        tr.innerHTML = `
            <td>${label}</td>
            <td>${fmtBytes(row.download)}</td>
            <td>${fmtBytes(row.upload)}</td>
            <td>${row.rtt_avg != null ? row.rtt_avg.toFixed(1) + ' ms' : '--'}</td>
            <td>${row.loss_pct != null ? row.loss_pct.toFixed(2) + '%' : '--'}</td>`;
        tr.addEventListener('click', () => {
            tbody.querySelectorAll('tr').forEach(r => r.classList.remove('selected'));
            tr.classList.add('selected');
            fetchBreakdownTS(kind, row.key);
        });
        tbody.appendChild(tr);
    });
}

function updateDetailChart(kind, key, data) {
    const canvasId = kind === 'app' ? 'chart-app-detail' : 'chart-dest-detail';
    const titleId = kind === 'app' ? 'app-chart-title' : 'dest-chart-title';
    document.getElementById(titleId).textContent = key;
    if (detailCharts[kind]) detailCharts[kind].destroy();
    if (!data || data.length === 0) return;
    const ts = data.map(d => new Date(d.ts * 1000));
    detailCharts[kind] = new Chart(document.getElementById(canvasId), {
        type: 'line',
        data: { datasets: [
            { label: 'Download', borderColor: C.download, data: data.map((d, i) => ({ x: ts[i], y: d.download })), yAxisID: 'y' },
            { label: 'Upload', borderColor: C.upload, data: data.map((d, i) => ({ x: ts[i], y: d.upload })), yAxisID: 'y' },
            { label: 'RTT', borderColor: C.rttAvg, data: data.map((d, i) => ({ x: ts[i], y: d.rtt_avg || 0 })), yAxisID: 'y1' },
        ] },
        options: {
            ...baseOpts,
            scales: {
                x: baseOpts.scales.x,
                y: { ...baseOpts.scales.y, position: 'left', ticks: { ...baseOpts.scales.y.ticks, callback: v => fmtBytes(v) } },
                y1: { beginAtZero: true, position: 'right', grid: { drawOnChartArea: false }, ticks: { color: '#8b949e', font: { size: 10 }, callback: v => v.toFixed(0) + 'ms' } }
            }
        }
    });
}

// --- Utilities ---

function fmtBytes(b) {
    if (b == null || b === 0) return '0 B';
    const u = ['B', 'KB', 'MB', 'GB'];
    const i = Math.min(Math.floor(Math.log(Math.abs(b)) / Math.log(1024)), u.length - 1);
    return (b / Math.pow(1024, i)).toFixed(1) + ' ' + u[i];
}

function fmtSpeed(bps) {
    const mbps = (bps * 8) / (1024 * 1024);
    if (mbps >= 1) return mbps.toFixed(1) + ' <span class="unit">Mbps</span>';
    const kbps = (bps * 8) / 1024;
    return kbps.toFixed(0) + ' <span class="unit">Kbps</span>';
}

function setRange(range) {
    currentRange = range;
    document.querySelectorAll('.range-selector button').forEach(b => b.classList.toggle('active', b.dataset.range === range));
    refreshCurrentTab();
    setupRefresh();
}

function setTab(tab) {
    currentTab = tab;
    document.querySelectorAll('.tab').forEach(t => t.classList.toggle('active', t.dataset.tab === tab));
    document.querySelectorAll('.tab-content').forEach(c => c.classList.toggle('active', c.id === `tab-${tab}`));
    refreshCurrentTab();
}

function refreshCurrentTab() {
    fetchStatus();
    switch (currentTab) {
        case 'overview': fetchMetrics(); break;
        case 'apps': fetchBreakdown('app'); break;
        case 'destinations': fetchBreakdown('dest'); break;
    }
}

function setupRefresh() {
    if (refreshInterval) clearInterval(refreshInterval);
    const intervals = { '1h': 2000, '24h': 10000, '7d': 30000, '30d': 60000, '90d': 60000 };
    refreshInterval = setInterval(() => refreshCurrentTab(), intervals[currentRange] || 5000);
}

// --- Init ---

document.addEventListener('DOMContentLoaded', () => {
    initCharts();
    fetchMetrics();
    fetchStatus();
    setupRefresh();

    document.querySelectorAll('.range-selector button').forEach(btn =>
        btn.addEventListener('click', () => setRange(btn.dataset.range)));
    document.querySelectorAll('.tab').forEach(tab =>
        tab.addEventListener('click', () => setTab(tab.dataset.tab)));
});
