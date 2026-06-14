const API_BASE = '';
let ws = null;
let currentBed = null;
let bedData = {};
let bedVitals = {};
let bedRisks = {};
let alerts = [];
let charts = {};
let miniChart = null;
let selectedBedForDetail = null;
let bedChart = null;
let riskHeatmap = null;

document.addEventListener('DOMContentLoaded', () => {
    initTabs();
    initBedChart();
    initCharts();
    initHeatmap();
    loadInitialData();
    connectWebSocket();
    updateTime();
    setInterval(updateTime, 1000);
    setInterval(loadStatistics, 5000);
});

function initTabs() {
    document.querySelectorAll('.tab-btn').forEach(btn => {
        btn.addEventListener('click', () => {
            const tab = btn.dataset.tab;
            document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
            document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
            btn.classList.add('active');
            document.getElementById('tab-' + tab).classList.add('active');

            if (tab === 'vitals' && charts.chartECG) {
                setTimeout(() => {
                    Object.values(charts).forEach(c => c && c.resize());
                }, 100);
            }
            if (tab === 'heatmap' && riskHeatmap) {
                setTimeout(() => {
                    riskHeatmap.resize();
                    loadHeatmapData();
                }, 100);
            }
        });
    });
}

function initBedChart() {
    bedChart = new BedChart('bedChartContainer');
    bedChart.onBedClick((bedId, bed) => {
        selectBed(bedId);
    });
}

function initHeatmap() {
    riskHeatmap = new RiskHeatmap('heatmapChart');
}

function selectBed(bedId) {
    selectedBedForDetail = bedId;
    const bed = bedData[bedId];
    if (!bed) return;

    document.getElementById('panelEmpty').style.display = 'none';
    document.getElementById('panelContent').style.display = 'block';

    document.getElementById('detailBedCode').textContent = bed.bed_code;
    document.getElementById('detailPatientName').textContent = bed.patient_name;
    document.getElementById('detailPatientAge').textContent = bed.patient_age;
    document.getElementById('detailPatientGender').textContent = bed.patient_gender;

    updateBedDetail(bedId);
    loadBedVitalsChart(bedId);
}

function updateBedDetail(bedId) {
    const vitals = bedVitals[bedId];
    const risk = bedRisks[bedId];

    if (vitals) {
        document.getElementById('vitalECG').textContent = vitals.ecg ? vitals.ecg.toFixed(1) : '--';
        document.getElementById('vitalVent').textContent = vitals.ventilator ? vitals.ventilator.toFixed(1) : '--';
        document.getElementById('vitalSpO2').textContent = vitals.spo2 ? vitals.spo2.toFixed(1) : '--';
        document.getElementById('vitalTemp').textContent = vitals.temperature ? vitals.temperature.toFixed(1) : '--';
    }

    if (risk) {
        document.getElementById('detailSOFA').textContent = risk.sofa_score.toFixed(1);
        document.getElementById('detailSepsis').textContent = (risk.sepsis_probability * 100).toFixed(1) + '%';
        document.getElementById('detailCRE').textContent = (risk.cre_risk * 100).toFixed(1) + '%';
        document.getElementById('detailMRSA').textContent = (risk.mrsa_risk * 100).toFixed(1) + '%';
    }
}

function initCharts() {
    charts.chartECG = echarts.init(document.getElementById('chartECG'), 'dark');
    charts.chartVent = echarts.init(document.getElementById('chartVent'), 'dark');
    charts.chartSpO2 = echarts.init(document.getElementById('chartSpO2'), 'dark');
    charts.chartTemp = echarts.init(document.getElementById('chartTemp'), 'dark');
    miniChart = echarts.init(document.getElementById('miniChart'), 'dark');

    const commonOption = (color, yMin, yMax) => ({
        backgroundColor: 'transparent',
        tooltip: { trigger: 'axis' },
        grid: { left: 50, right: 20, top: 20, bottom: 30 },
        xAxis: {
            type: 'time',
            axisLine: { lineStyle: { color: '#2a4a7a' } },
            axisLabel: { color: '#8b9bb4', fontSize: 10 }
        },
        yAxis: {
            type: 'value',
            min: yMin,
            max: yMax,
            axisLine: { lineStyle: { color: '#2a4a7a' } },
            axisLabel: { color: '#8b9bb4', fontSize: 10 },
            splitLine: { lineStyle: { color: 'rgba(42, 74, 122, 0.3)' } }
        },
        series: [{
            type: 'line',
            smooth: true,
            showSymbol: false,
            lineStyle: { color, width: 2 },
            areaStyle: {
                color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
                    { offset: 0, color: color + '66' },
                    { offset: 1, color: color + '00' }
                ])
            },
            data: []
        }]
    });

    charts.chartECG.setOption(commonOption('#ef4444', 40, 180));
    charts.chartVent.setOption(commonOption('#8b5cf6', 5, 40));
    charts.chartSpO2.setOption(commonOption('#22c55e', 70, 100));
    charts.chartTemp.setOption(commonOption('#f97316', 35, 42));

    miniChart.setOption({
        backgroundColor: 'transparent',
        tooltip: { trigger: 'axis' },
        legend: {
            data: ['心率', '血氧'],
            textStyle: { color: '#8b9bb4', fontSize: 10 },
            top: 0
        },
        grid: { left: 40, right: 15, top: 25, bottom: 25 },
        xAxis: {
            type: 'time',
            axisLine: { lineStyle: { color: '#2a4a7a' } },
            axisLabel: { color: '#8b9bb4', fontSize: 9 }
        },
        yAxis: {
            type: 'value',
            axisLine: { lineStyle: { color: '#2a4a7a' } },
            axisLabel: { color: '#8b9bb4', fontSize: 9 },
            splitLine: { lineStyle: { color: 'rgba(42, 74, 122, 0.3)' } }
        },
        series: [
            { name: '心率', type: 'line', smooth: true, showSymbol: false,
              lineStyle: { color: '#ef4444', width: 1.5 }, data: [] },
            { name: '血氧', type: 'line', smooth: true, showSymbol: false,
              lineStyle: { color: '#22c55e', width: 1.5 }, data: [], yAxisIndex: 0 }
        ]
    });

    document.getElementById('bedSelect').addEventListener('change', loadVitalsForSelectedBed);
    document.getElementById('timeRange').addEventListener('change', loadVitalsForSelectedBed);

    window.addEventListener('resize', () => {
        Object.values(charts).forEach(c => c && c.resize());
        if (miniChart) miniChart.resize();
        if (riskHeatmap) riskHeatmap.resize();
    });
}

function loadInitialData() {
    fetch(API_BASE + '/api/beds')
        .then(r => r.json())
        .then(data => {
            data.forEach(bed => {
                bedData[bed.id] = bed;
            });
            populateBedSelect(data);
            if (bedChart) {
                bedChart.setBeds(data);
            }
            if (riskHeatmap) {
                riskHeatmap.setBeds(data);
            }
        });

    loadStatistics();
    loadAlerts();
}

function populateBedSelect(beds) {
    const select = document.getElementById('bedSelect');
    beds.forEach(bed => {
        const opt = document.createElement('option');
        opt.value = bed.id;
        opt.textContent = `${bed.bed_code} - ${bed.patient_name}`;
        select.appendChild(opt);
    });
}

function loadStatistics() {
    fetch(API_BASE + '/api/statistics')
        .then(r => r.json())
        .then(s => {
            document.getElementById('totalBeds').textContent = s.total_beds;
            document.getElementById('occupiedBeds').textContent = s.occupied_beds;
            document.getElementById('activeAlerts').textContent = s.active_alerts;
            document.getElementById('highRiskSepsis').textContent = s.high_risk_sepsis;
            document.getElementById('highRiskInfection').textContent = s.high_risk_infection;
            document.getElementById('avgSOFA').textContent = s.avg_sofa_score.toFixed(1);
        });
}

function loadAlerts() {
    fetch(API_BASE + '/api/alerts/active')
        .then(r => r.json())
        .then(data => {
            alerts = data;
            renderAlerts();
        });
}

function renderAlerts() {
    const filter = document.querySelector('.filter-btn.active')?.dataset.filter || 'all';
    const list = document.getElementById('alertsList');

    let filtered = alerts;
    if (filter === 'unack') {
        filtered = alerts.filter(a => !a.acknowledged);
    } else if (filter !== 'all') {
        filtered = alerts.filter(a => a.alert_type === filter);
    }

    if (filtered.length === 0) {
        list.innerHTML = '<div class="alerts-empty">暂无告警</div>';
        return;
    }

    list.innerHTML = filtered.map(alert => {
        const icons = {
            sepsis: '🚨',
            cre_infection: '🦠',
            mrsa_infection: '🧫'
        };
        const types = {
            sepsis: '脓毒症预警',
            cre_infection: 'CRE感染风险',
            mrsa_infection: 'MRSA感染风险'
        };
        return `
        <div class="alert-item severity-${alert.severity} ${alert.acknowledged ? 'acknowledged' : ''}">
            <div class="alert-icon">${icons[alert.alert_type] || '⚠️'}</div>
            <div class="alert-main">
                <div class="alert-type">${types[alert.alert_type] || alert.alert_type} · ${alert.severity.toUpperCase()}</div>
                <div class="alert-message">${alert.message}</div>
            </div>
            <div class="alert-meta">
                <div class="alert-bed">ICU-${String(alert.bed_id).padStart(3, '0')}</div>
                <div class="alert-value">触发值: ${alert.trigger_value.toFixed(2)} / 阈值: ${alert.threshold}</div>
                <div>${new Date(alert.created_at).toLocaleString('zh-CN')}</div>
            </div>
            <div class="alert-actions">
                ${!alert.acknowledged ? `<button class="btn btn-primary" style="font-size:11px;padding:5px 10px;" onclick="acknowledgeAlert(${alert.id})">确认</button>` : `<span style="color:#22c55e;font-size:11px;">已确认</span>`}
            </div>
        </div>`;
    }).join('');

    document.querySelectorAll('.filter-btn').forEach(btn => {
        btn.onclick = () => {
            document.querySelectorAll('.filter-btn').forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            renderAlerts();
        };
    });
}

function acknowledgeAlert(id) {
    fetch(API_BASE + `/api/alerts/${id}/acknowledge`, { method: 'POST' })
        .then(r => r.json())
        .then(() => loadAlerts());
}

function loadVitalsForSelectedBed() {
    const bedId = document.getElementById('bedSelect').value;
    if (!bedId) return;

    const seconds = document.getElementById('timeRange').value;
    fetch(API_BASE + `/api/beds/${bedId}/vitals/recent?seconds=${seconds}`)
        .then(r => r.json())
        .then(data => {
            updateChart(charts.chartECG, data.ecg || []);
            updateChart(charts.chartVent, data.ventilator || []);
            updateChart(charts.chartSpO2, data.spo2 || []);
            updateChart(charts.chartTemp, data.temperature || []);
        });
}

function updateChart(chart, points) {
    const data = points.map(p => [new Date(p.time).getTime(), p.value]);
    chart.setOption({ series: [{ data }] });
}

function loadBedVitalsChart(bedId) {
    fetch(API_BASE + `/api/beds/${bedId}/vitals/recent?seconds=300`)
        .then(r => r.json())
        .then(data => {
            const ecgData = (data.ecg || []).map(p => [new Date(p.time).getTime(), p.value]);
            const spo2Data = (data.spo2 || []).map(p => [new Date(p.time).getTime(), p.value]);
            miniChart.setOption({
                series: [
                    { data: ecgData },
                    { data: spo2Data }
                ]
            });
        });
}

function loadHeatmapData() {
    fetch(API_BASE + '/api/infection/risk')
        .then(r => r.json())
        .then(data => {
            if (riskHeatmap) {
                riskHeatmap.setRiskData(data);
            }
        });
}

function connectWebSocket() {
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${location.host}/ws`;

    ws = new WebSocket(wsUrl);

    ws.onopen = () => {
        document.getElementById('connectionStatus').textContent = '● 已连接';
        document.getElementById('connectionStatus').className = 'status-badge connected';
    };

    ws.onclose = () => {
        document.getElementById('connectionStatus').textContent = '● 已断开';
        document.getElementById('connectionStatus').className = 'status-badge disconnected';
        setTimeout(connectWebSocket, 3000);
    };

    ws.onerror = () => {
        document.getElementById('connectionStatus').textContent = '● 连接错误';
        document.getElementById('connectionStatus').className = 'status-badge disconnected';
    };

    ws.onmessage = (e) => {
        try {
            const msg = JSON.parse(e.data);
            if (msg.type === 'vitals_update') {
                handleVitalsUpdate(msg.data);
            } else if (msg.type === 'alert') {
                handleNewAlert(msg.data);
            }
        } catch (err) {
            console.error('WS解析错误:', err);
        }
    };
}

function handleVitalsUpdate(data) {
    data.forEach(item => {
        const bedId = item.bed.id;
        if (item.vitals) bedVitals[bedId] = item.vitals;
        if (item.risk) bedRisks[bedId] = item.risk;
    });

    if (bedChart) {
        bedChart.setVitals(bedVitals);
        bedChart.setRisks(bedRisks);
    }

    if (selectedBedForDetail) {
        updateBedDetail(selectedBedForDetail);
    }
}

function handleNewAlert(alert) {
    alerts.unshift(alert);
    renderAlerts();
    loadStatistics();

    const toast = document.getElementById('alertToast');
    const typeNames = {
        sepsis: '脓毒症预警',
        cre_infection: 'CRE感染风险',
        mrsa_infection: 'MRSA感染风险'
    };
    toast.innerHTML = `
        <h4>⚠️ ${typeNames[alert.alert_type] || '告警'} - 床位ICU-${String(alert.bed_id).padStart(3, '0')}</h4>
        <p>${alert.message}</p>
    `;
    toast.classList.remove('hidden');
    setTimeout(() => toast.classList.add('hidden'), 5000);
}

function updateTime() {
    document.getElementById('currentTime').textContent = new Date().toLocaleString('zh-CN');
}

function showDetailVitals() {
    if (selectedBedForDetail) {
        document.querySelector('.tab-btn[data-tab="vitals"]').click();
        document.getElementById('bedSelect').value = selectedBedForDetail;
        loadVitalsForSelectedBed();
    }
}

function recordProcedure() {
    if (!selectedBedForDetail) return;
    const procType = prompt('请输入操作类型（如：气管插管、中心静脉置管等）:');
    if (!procType) return;

    fetch(API_BASE + `/api/beds/${selectedBedForDetail}/invasive`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
            bed_id: selectedBedForDetail,
            procedure_type: procType,
            procedure_time: new Date().toISOString(),
            notes: '前端记录'
        })
    }).then(r => r.json()).then(d => alert('操作已记录'));
}

function recordAntibiotic() {
    if (!selectedBedForDetail) return;
    const abType = prompt('请输入抗生素类型（如：美罗培南、万古霉素等）:');
    if (!abType) return;
    const dosage = parseFloat(prompt('请输入剂量(g):', '1.0') || '1');

    fetch(API_BASE + `/api/beds/${selectedBedForDetail}/antibiotics`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
            bed_id: selectedBedForDetail,
            antibiotic_type: abType,
            dosage: dosage,
            start_date: new Date().toISOString(),
            end_date: new Date(Date.now() + 7 * 86400000).toISOString()
        })
    }).then(r => r.json()).then(d => alert('抗生素已记录'));
}
