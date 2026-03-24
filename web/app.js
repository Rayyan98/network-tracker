let currentRange = '1h';
let currentTab = 'overview';
let charts = {};
let detailCharts = {};
let refreshInterval = null;

const C = {
    download: '#00d4ff',     // bright cyan
    upload: '#00e676',       // bright green
    rttAvg: '#b388ff',      // electric purple
    rttP95: '#ff9100',      // hot orange
    jitter: '#ffab00',      // vivid amber
    loss: '#ff5252',        // bright red
    flows: '#4facfe',       // sky blue
    dns: '#64b5f6',         // light blue
    quality: '#00e676',
    qualityBg: 'rgba(0, 230, 118, 0.12)',
    // UDP variants — same hue, lower opacity
    udpDown: 'rgba(0, 212, 255, 0.4)',
    udpUp: 'rgba(0, 230, 118, 0.4)',
};

// --- Threshold line helpers ---
function thresholdLine(value, color, label) {
    return {
        type: 'line',
        yMin: value, yMax: value,
        yScaleID: 'y',
        adjustScaleRange: false,
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
        yScaleID: 'y',
        adjustScaleRange: false, // DON'T expand axis to fit the band
        backgroundColor: color,
        borderWidth: 0,
    };
}

function thresholdLineNoExpand(value, color, label) {
    return {
        type: 'line',
        yMin: value, yMax: value,
        yScaleID: 'y',
        adjustScaleRange: false, // DON'T expand axis to fit the line
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

// Band colors — Grafana-style: 25-40% opacity so zones are unmistakable
const BG = 'rgba(0,200,83,';    // green
const BO = 'rgba(255,160,0,';   // amber
const BR = 'rgba(255,23,68,';   // red
const BX = 'rgba(183,28,28,';   // deep red

const annotations = {
    quality: {
        // R-factor: 80+ excellent, 60-80 good, <60 poor
        lineGood:     thresholdLine(80, BG+'0.8)', 'Good (R>80)'),
        lineFair:     thresholdLine(60, BO+'0.8)', 'Fair (R=60)'),
    },
    latency: {
        // ITU-T G.114: <150ms one-way = <75ms RTT great, <300ms RTT acceptable
        bandGreat:    thresholdBand(0, 75,       BG+'0.25)'),
        bandOK:       thresholdBand(75, 150,     BO+'0.20)'),
        bandBad:      thresholdBand(150, 10000,  BR+'0.22)'),
        lineGreat:    thresholdLine(75,  BG+'0.8)', '75ms (ITU Great)'),
        lineBad:      thresholdLine(150, BR+'0.8)', '150ms (ITU Limit)'),
    },
    jitter: {
        // ITU VoIP: <20ms good, 20-50ms acceptable, >50ms bad
        bandGreat:    thresholdBand(0, 20,     BG+'0.25)'),
        bandOK:       thresholdBand(20, 50,    BO+'0.20)'),
        bandBad:      thresholdBand(50, 10000, BR+'0.22)'),
        lineGreat:    thresholdLine(20, BG+'0.8)', '20ms'),
        lineBad:      thresholdLine(50, BR+'0.8)', '50ms'),
    },
    loss: {
        // ITU VoIP: <1% acceptable, 1-2.5% degraded, >2.5% bad
        bandGreat:    thresholdBand(0, 1,     BG+'0.25)'),
        bandOK:       thresholdBand(1, 2.5,   BO+'0.20)'),
        bandBad:      thresholdBand(2.5, 100, BR+'0.22)'),
        lineGreat:    thresholdLine(1,   BG+'0.8)', '1%'),
        lineBad:      thresholdLine(2.5, BR+'0.8)', '2.5%'),
    },
    dns: {
        // Industry: <50ms fast, 50-200ms acceptable, >200ms slow
        bandGreat:    thresholdBand(0, 50,       BG+'0.25)'),
        bandOK:       thresholdBand(50, 200,     BO+'0.20)'),
        bandBad:      thresholdBand(200, 10000,  BR+'0.22)'),
        lineGreat:    thresholdLine(50,  BG+'0.8)', '50ms'),
        lineBad:      thresholdLine(200, BR+'0.8)', '200ms'),
    },
    download: {
        bandBad:      thresholdBand(0,              1*1024*1024,  BR+'0.18)'),
        bandOK:       thresholdBand(1*1024*1024,    5*1024*1024,  BO+'0.15)'),
        bandGood:     thresholdBand(5*1024*1024,   25*1024*1024,  BG+'0.12)'),
        bandGreat:    thresholdBand(25*1024*1024,   1e12,         BG+'0.25)'),
        lineHD:       thresholdLine(3*1024*1024,  'rgba(0,212,255,0.6)', '3 MB/s HD Video'),
        line4K:       thresholdLine(25*1024*1024, 'rgba(179,136,255,0.6)', '25 MB/s 4K'),
    },
    upload: {
        bandBad:      thresholdBand(0,             512*1024,      BR+'0.18)'),
        bandOK:       thresholdBand(512*1024,      2*1024*1024,   BO+'0.15)'),
        bandGood:     thresholdBand(2*1024*1024,  10*1024*1024,   BG+'0.12)'),
        bandGreat:    thresholdBand(10*1024*1024,  1e12,          BG+'0.25)'),
        lineVideo:    thresholdLine(3*1024*1024,  'rgba(0,230,118,0.6)', '3 MB/s Video Call'),
        lineStream:   thresholdLine(5*1024*1024,  'rgba(179,136,255,0.6)', '5 MB/s Game Stream'),
    },
};

// --- Base options ---
const baseOpts = {
    responsive: true, maintainAspectRatio: false,
    animation: { duration: 300 },
    plugins: {
        legend: { labels: { color: '#8b9bb0', boxWidth: 12, padding: 10, font: { size: 11 } } },
        tooltip: {
            mode: 'index', intersect: false,
            backgroundColor: '#1a2332ee',
            titleColor: '#e6edf3',
            bodyColor: '#8b9bb0',
            borderColor: '#243044',
            borderWidth: 1,
            padding: 10,
            cornerRadius: 6,
        },
        annotation: { annotations: {} }
    },
    scales: {
        x: { type: 'time', grid: { color: '#1a233280' }, ticks: { color: '#5d6f85', maxTicksLimit: 8, font: { size: 10 } } },
        y: { beginAtZero: true, grid: { color: '#1a233280' }, ticks: { color: '#5d6f85', font: { size: 10 } } }
    },
    elements: { point: { radius: 0 }, line: { borderWidth: 2, tension: 0.3, spanGaps: false } }
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

// Custom plugin: draws a vertical gradient background (red bottom -> yellow mid -> green top)
const qualityGradientPlugin = {
    id: 'qualityGradient',
    beforeDraw(chart) {
        const { ctx, chartArea, scales } = chart;
        if (!chartArea || !scales.y) return;
        const { left, right, top, bottom } = chartArea;

        const grad = ctx.createLinearGradient(0, bottom, 0, top);
        grad.addColorStop(0,    'rgba(183, 28, 28, 0.40)');   // 0   - strong red
        grad.addColorStop(0.25, 'rgba(255, 23, 68, 0.28)');   // 25  - red
        grad.addColorStop(0.50, 'rgba(255, 160, 0, 0.22)');   // 50  - amber
        grad.addColorStop(0.65, 'rgba(255, 160, 0, 0.12)');   // 65  - fading amber
        grad.addColorStop(0.80, 'rgba(0, 200, 83, 0.12)');    // 80  - entering green
        grad.addColorStop(1,    'rgba(0, 200, 83, 0.35)');    // 100 - strong green

        ctx.save();
        ctx.fillStyle = grad;
        ctx.fillRect(left, top, right - left, bottom - top);
        ctx.restore();
    }
};

function initCharts() {
    charts.quality = new Chart(document.getElementById('chart-quality'), {
        type: 'line',
        data: { datasets: [{ label: 'Quality', borderColor: C.quality, backgroundColor: C.qualityBg, fill: true, data: [] }] },
        plugins: [qualityGradientPlugin],
        options: {
            ...baseOpts,
            plugins: {
                ...baseOpts.plugins,
                annotation: { annotations: annotations.quality },
                tooltip: { ...baseOpts.plugins.tooltip, ...tooltipWithBand(v => qualityBand(v)) }
            },
            scales: { ...baseOpts.scales, y: { ...baseOpts.scales.y, min: 0, max: 100, ticks: { ...baseOpts.scales.y.ticks, callback: v => v } } }
        }
    });

    charts.download = new Chart(document.getElementById('chart-download'), {
        type: 'line',
        data: { datasets: [
            { label: 'TCP', borderColor: C.download, data: [] },
            { label: 'UDP', borderColor: C.udpDown, borderDash: [4,4], data: [] },
            { label: 'Link Speed', borderColor: '#fff', borderWidth: 0, pointRadius: 2, pointBackgroundColor: '#fff', showLine: false, data: [] }
        ] },
        options: makeOpts(v => fmtBytes(v) + '/s', annotations.download,
            tooltipWithBand(v => ratingDirect(v, 25*1024*1024, 10*1024*1024, 3*1024*1024)))
    });

    charts.upload = new Chart(document.getElementById('chart-upload'), {
        type: 'line',
        data: { datasets: [
            { label: 'TCP', borderColor: C.upload, data: [] },
            { label: 'UDP', borderColor: C.udpUp, borderDash: [4,4], data: [] },
            { label: 'Link Speed', borderColor: '#fff', borderWidth: 0, pointRadius: 2, pointBackgroundColor: '#fff', showLine: false, data: [] }
        ] },
        options: makeOpts(v => fmtBytes(v) + '/s', annotations.upload,
            tooltipWithBand(v => ratingDirect(v, 5*1024*1024, 2*1024*1024, 500*1024)))
    });

    charts.latency = new Chart(document.getElementById('chart-latency'), {
        type: 'line',
        data: { datasets: [
            { label: 'Avg', borderColor: C.rttAvg, data: [] },
            { label: 'P95', borderColor: C.rttP95, data: [] }
        ] },
        options: makeOpts(v => v.toFixed(0) + 'ms', annotations.latency,
            tooltipWithBand(v => ratingInverse(v, 75, 150, 300)))
    });

    charts.jitter = new Chart(document.getElementById('chart-jitter'), {
        type: 'line',
        data: { datasets: [{ label: 'Jitter', borderColor: C.jitter, backgroundColor: C.jitter + '20', fill: true, data: [] }] },
        options: makeOpts(v => v.toFixed(1) + 'ms', annotations.jitter,
            tooltipWithBand(v => ratingInverse(v, 20, 50, 100)))
    });

    charts.loss = new Chart(document.getElementById('chart-loss'), {
        type: 'line',
        data: { datasets: [{ label: 'Packet Loss', borderColor: C.loss, backgroundColor: C.loss + '20', fill: true, data: [] }] },
        options: makeOpts(v => v.toFixed(1) + '%', annotations.loss,
            tooltipWithBand(v => ratingInverse(v, 1.0, 2.5, 5.0)))
    });

    charts.dns = new Chart(document.getElementById('chart-dns'), {
        type: 'line',
        data: { datasets: [{ label: 'DNS', borderColor: C.dns, backgroundColor: C.dns + '20', fill: true, data: [] }] },
        options: makeOpts(v => v.toFixed(0) + 'ms', annotations.dns,
            tooltipWithBand(v => ratingInverse(v, 50, 200, 500)))
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
    charts.download.data.datasets[2].data = data.map((d, i) => ({ x: ts[i], y: d.transfer_down_avg > 0 ? d.transfer_down_avg : null }));
    charts.download.update();

    const tcpUp = data.map(d => (d.upload_avg || 0) - (d.udp_upload_avg || 0));
    charts.upload.data.datasets[0].data = data.map((d, i) => ({ x: ts[i], y: Math.max(0, tcpUp[i]) }));
    charts.upload.data.datasets[1].data = data.map((d, i) => ({ x: ts[i], y: d.udp_upload_avg || 0 }));
    charts.upload.data.datasets[2].data = data.map((d, i) => ({ x: ts[i], y: d.transfer_up_avg > 0 ? d.transfer_up_avg : null }));
    charts.upload.update();

    // Read selected measures from dropdowns
    const rttMeasure = document.getElementById('measure-latency').value;
    const jitterMeasure = document.getElementById('measure-jitter').value;
    const lossMeasure = document.getElementById('measure-loss').value;
    const dnsMeasure = document.getElementById('measure-dns').value;

    // Helper: get field from data point, return null if missing
    const getField = (d, field) => d[field] != null ? d[field] : null;

    // Latency: show selected measure as primary line, plus a secondary reference
    charts.latency.data.datasets[0].data = data.map((d, i) => ({ x: ts[i], y: getField(d, rttMeasure) }));
    // Show avg as faded reference line if not already selected
    const rttRef = rttMeasure === 'rtt_avg' ? 'rtt_p95' : 'rtt_avg';
    charts.latency.data.datasets[1].data = data.map((d, i) => ({ x: ts[i], y: getField(d, rttRef) }));
    charts.latency.data.datasets[0].label = rttMeasure.replace('rtt_', '').toUpperCase();
    charts.latency.data.datasets[1].label = rttRef.replace('rtt_', '').toUpperCase();
    charts.latency.update();

    charts.jitter.data.datasets[0].data = data.map((d, i) => ({ x: ts[i], y: getField(d, jitterMeasure) }));
    charts.jitter.data.datasets[0].label = jitterMeasure.replace('jitter_', '').toUpperCase();
    charts.jitter.update();

    charts.loss.data.datasets[0].data = data.map((d, i) => ({ x: ts[i], y: getField(d, lossMeasure) }));
    charts.loss.data.datasets[0].label = lossMeasure.replace('loss_', '').toUpperCase();
    charts.loss.update();

    charts.dns.data.datasets[0].data = data.map((d, i) => ({ x: ts[i], y: getField(d, dnsMeasure) }));
    charts.dns.data.datasets[0].label = dnsMeasure.replace('dns_', '').toUpperCase();
    charts.dns.update();

    // Current values — show the selected measure
    const L = data[data.length - 1];
    document.getElementById('download-value').innerHTML = fmtSpeed(L.download_avg || 0);
    document.getElementById('upload-value').innerHTML = fmtSpeed(L.upload_avg || 0);
    document.getElementById('transfer-down-value').innerHTML = L.transfer_down_avg > 0 ? fmtSpeed(L.transfer_down_avg) : '--';
    document.getElementById('transfer-up-value').innerHTML = L.transfer_up_avg > 0 ? fmtSpeed(L.transfer_up_avg) : '--';

    const latVal = getField(L, rttMeasure);
    document.getElementById('latency-value').innerHTML = (latVal != null ? latVal.toFixed(1) : '--') + ' <span class="unit">ms</span>';
    const jitVal = getField(L, jitterMeasure);
    document.getElementById('jitter-value').innerHTML = (jitVal != null ? jitVal.toFixed(1) : '--') + ' <span class="unit">ms</span>';
    const lossVal = getField(L, lossMeasure);
    document.getElementById('loss-value').innerHTML = (lossVal != null ? lossVal.toFixed(2) : '--') + ' <span class="unit">%</span>';
    const dnsVal = getField(L, dnsMeasure);
    document.getElementById('dns-value').innerHTML = (dnsVal != null ? dnsVal.toFixed(0) : '--') + ' <span class="unit">ms</span>';

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

    setUC('hd_video_call', down, up, rtt, jitter, loss, 3.6*1024*1024, 3.2*1024*1024, 150, 30, 1.0);
    setUC('audio_call', down, up, rtt, jitter, loss, 100*1024, 100*1024, 300, 50, 2.0);
    setUC('screen_sharing', down, up, rtt, jitter, loss, 2*1024*1024, 3*1024*1024, 200, 50, 2.0);
    setUC('game_streaming', down, up, rtt, jitter, loss, 5*1024*1024, 6*1024*1024, 100, 20, 1.0);
    setUC('4k_streaming', down, up, rtt, jitter, loss, 25*1024*1024, 0, 0, 0, 5.0);
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
                y1: { beginAtZero: true, position: 'right', grid: { drawOnChartArea: false }, ticks: { color: '#5d6f85', font: { size: 10 }, callback: v => v.toFixed(0) + 'ms' } }
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
    const intervals = { '1m': 1000, '5m': 1000, '15m': 1000, '30m': 2000, '1h': 2000, '6h': 5000, '12h': 5000, '24h': 10000, '3d': 15000, '7d': 30000, '30d': 60000, '90d': 60000 };
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

    // Measure dropdowns: re-render charts on change (uses cached data via fetchMetrics)
    document.querySelectorAll('.measure-select').forEach(sel =>
        sel.addEventListener('change', () => fetchMetrics()));
});
