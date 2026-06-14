(function (global) {
    'use strict';

    /**
     * VAP风险条形图组件
     * 展示各床位VAP风险概率、HR风险比、时变特征（趋势/波动率）叠加
     * 用法：
     *   const vapBar = new VAPRiskBar('domId', { height: 400 });
     *   vapBar.setData([{bedId, riskProb, hazardsRatio, features, onsetHours}]);
     *   vapBar.resize();
     */
    function VAPRiskBar(domId, options) {
        this.dom = document.getElementById(domId);
        if (!this.dom) throw new Error('VAPRiskBar: DOM #' + domId + ' not found');
        this.chart = echarts.init(this.dom, 'dark', {
            renderer: 'canvas',
            height: (options && options.height) || 400
        });
        this.data = [];
        this._initBaseOption();
        var self = this;
        this._resizeHandler = function () { self.resize(); };
        window.addEventListener('resize', this._resizeHandler);
    }

    VAPRiskBar.prototype._initBaseOption = function () {
        var self = this;
        this.baseOption = {
            backgroundColor: 'transparent',
            title: {
                text: 'VAP呼吸机相关性肺炎风险评估',
                subtext: '条形=风险概率 · 折线=HR风险比 · 红色虚线=0.7阈值',
                left: 'center',
                textStyle: { color: '#fff', fontSize: 14 },
                subtextStyle: { color: '#aaa', fontSize: 11 }
            },
            tooltip: {
                trigger: 'axis',
                axisPointer: { type: 'shadow' },
                formatter: function (params) { return self._formatTooltip(params); }
            },
            legend: {
                data: ['风险概率', 'HR风险比', '时变协变量贡献'],
                top: 40,
                textStyle: { color: '#ccc' }
            },
            grid: { left: '8%', right: '8%', bottom: '12%', top: 90 },
            xAxis: {
                type: 'category',
                data: [],
                axisLabel: { color: '#ccc', rotate: 45 },
                axisLine: { lineStyle: { color: '#555' } }
            },
            yAxis: [
                {
                    type: 'value', name: '风险概率', min: 0, max: 1,
                    axisLabel: { color: '#4fc3f7', formatter: function (v) { return (v * 100).toFixed(0) + '%'; } },
                    splitLine: { lineStyle: { color: '#333' } }
                },
                {
                    type: 'value', name: 'HR风险比',
                    axisLabel: { color: '#ffb74d' },
                    splitLine: { show: false }
                }
            ],
            series: [
                {
                    name: '风险概率', type: 'bar', data: [],
                    itemStyle: {
                        color: function (params) {
                            var v = params.value;
                            if (v >= 0.7) return {
                                type: 'linear', x: 0, y: 0, x2: 0, y2: 1,
                                colorStops: [{ offset: 0, color: '#ef5350' }, { offset: 1, color: '#b71c1c' }]
                            };
                            if (v >= 0.4) return '#ffa726';
                            return '#66bb6a';
                        },
                        borderRadius: [4, 4, 0, 0]
                    },
                    barMaxWidth: 40,
                    markLine: {
                        silent: true, symbol: 'none',
                        lineStyle: { color: '#ef5350', type: 'dashed', width: 2 },
                        data: [{ yAxis: 0.7, label: { formatter: '阈值0.7', color: '#ef5350' } }]
                    }
                },
                {
                    name: 'HR风险比', type: 'line', yAxisIndex: 1, data: [],
                    lineStyle: { color: '#ffb74d', width: 2, type: 'solid' },
                    itemStyle: { color: '#ffb74d' },
                    symbol: 'circle', symbolSize: 8
                },
                {
                    name: '时变协变量贡献', type: 'scatter', data: [],
                    symbolSize: function (val) { return Math.max(8, val[2] * 100); },
                    itemStyle: { color: 'rgba(100, 181, 246, 0.6)', borderColor: '#64b5f6', borderWidth: 1 },
                    tooltip: {
                        formatter: function (p) {
                            return '床位' + p.value[0] + '<br/>时变贡献: ' + (p.value[2] * 100).toFixed(1) + '%';
                        }
                    }
                }
            ]
        };
        this.chart.setOption(this.baseOption);
    };

    VAPRiskBar.prototype.setData = function (bedRisks) {
        this.data = (bedRisks || []).slice().sort(function (a, b) { return b.riskProb - a.riskProb; });
        var beds = this.data.map(function (d) { return d.bedId + '床'; });
        var probs = this.data.map(function (d) { return d.riskProb; });
        var hrs = this.data.map(function (d) { return d.hazardsRatio; });
        var tvcKeys = ['peak_pressure_trend', 'peak_pressure_volatility', 'oral_secretion_trend',
            'tidal_dev_recent', 'hours_accumulated'];
        var tvc = this.data.map(function (d) {
            var tv = d.features || {};
            var contrib = 0;
            tvcKeys.forEach(function (k) { contrib += Math.abs(tv[k] || 0); });
            return [d.bedId, d.riskProb, Math.min(0.5, contrib / 10)];
        });

        this.chart.setOption({
            xAxis: { data: beds },
            series: [
                { data: probs },
                { data: hrs },
                { data: tvc }
            ]
        });
    };

    VAPRiskBar.prototype._formatTooltip = function (params) {
        var html = '<b>' + params[0].axisValue + '</b><br/>';
        params.forEach(function (p) {
            if (p.seriesName === '风险概率') {
                html += p.marker + p.seriesName + ': <b style="color:#4fc3f7">' + (p.value * 100).toFixed(1) + '%</b><br/>';
            } else if (p.seriesName === 'HR风险比') {
                html += p.marker + p.seriesName + ': <b style="color:#ffb74d">' + p.value.toFixed(2) + '</b><br/>';
            }
        });
        return html;
    };

    VAPRiskBar.prototype.resize = function () { if (this.chart) this.chart.resize(); };

    VAPRiskBar.prototype.dispose = function () {
        if (this._resizeHandler) window.removeEventListener('resize', this._resizeHandler);
        if (this.chart) { this.chart.dispose(); this.chart = null; }
    };

    global.VAPRiskBar = VAPRiskBar;
    if (typeof module !== 'undefined') module.exports = VAPRiskBar;

})(window);
