(function (global) {
    'use strict';

    function RiskHeatmap(containerId, options) {
        this.container = document.getElementById(containerId);
        if (!this.container) {
            console.error('RiskHeatmap: container not found:', containerId);
            return;
        }

        this.options = Object.assign({
            rows: 5,
            cols: 10,
            min: 0,
            max: 1,
            colors: ['#22c55e', '#84cc16', '#eab308', '#f97316', '#ef4444']
        }, options || {});

        this.chart = echarts.init(this.container, 'dark');
        this.beds = {};
        this.riskData = [];

        this._initOption();
        this._bindResize();
    }

    RiskHeatmap.prototype._initOption = function () {
        var self = this;
        var xData = [];
        var yData = [];

        for (var i = 0; i < this.options.cols; i++) {
            xData.push('列' + (i + 1));
        }
        for (var i = 0; i < this.options.rows; i++) {
            yData.push('病区' + (i + 1));
        }

        this.chart.setOption({
            backgroundColor: 'transparent',
            tooltip: {
                formatter: function (p) {
                    if (p.data && p.data.value) {
                        var val = Array.isArray(p.data.value) ? p.data.value[2] : p.data.value;
                        return '床位: ICU-' + String(p.data.bedId).padStart(3, '0') + '<br/>' +
                            'CRE风险: ' + ((p.data.cre || 0) * 100).toFixed(1) + '%<br/>' +
                            'MRSA风险: ' + ((p.data.mrsa || 0) * 100).toFixed(1) + '%<br/>' +
                            '综合风险: ' + (val * 100).toFixed(1) + '%';
                    }
                    return '';
                }
            },
            visualMap: {
                min: this.options.min,
                max: this.options.max,
                calculable: true,
                orient: 'horizontal',
                left: 'center',
                bottom: 10,
                textStyle: { color: '#8b9bb4' },
                inRange: {
                    color: this.options.colors
                }
            },
            grid: { left: 60, right: 60, top: 30, bottom: 60 },
            xAxis: {
                type: 'category',
                data: xData,
                axisLine: { lineStyle: { color: '#2a4a7a' } },
                axisLabel: { color: '#8b9bb4' }
            },
            yAxis: {
                type: 'category',
                data: yData,
                axisLine: { lineStyle: { color: '#2a4a7a' } },
                axisLabel: { color: '#8b9bb4' }
            },
            series: [{
                name: '感染风险',
                type: 'heatmap',
                label: {
                    show: true,
                    color: '#fff',
                    fontSize: 11,
                    formatter: function (p) {
                        var val = Array.isArray(p.data.value) ? p.data.value[2] : p.data.value;
                        return 'ICU-' + String(p.data.bedId).padStart(3, '0') + '\n' + (val * 100).toFixed(0) + '%';
                    }
                },
                data: []
            }]
        });
    };

    RiskHeatmap.prototype.setBeds = function (bedList) {
        var self = this;
        bedList.forEach(function (bed) {
            self.beds[bed.id] = bed;
        });
    };

    RiskHeatmap.prototype.setRiskData = function (riskPoints) {
        var self = this;
        var heatData = riskPoints.map(function (p) {
            var bedId = p.bed_id !== undefined ? p.bed_id : p.bedId;
            var bed = self.beds[bedId];
            var row = 0;
            var col = 0;
            if (bed) {
                row = Math.floor((bedId - 1) / 10);
                col = (bedId - 1) % 10;
            } else {
                row = Math.floor((bedId - 1) / self.options.cols);
                col = (bedId - 1) % self.options.cols;
            }
            var cre = p.cre_risk !== undefined ? p.cre_risk : (p.cre || 0);
            var mrsa = p.mrsa_risk !== undefined ? p.mrsa_risk : (p.mrsa || 0);
            var maxVal = p.max_risk !== undefined ? p.max_risk : (p.maxRisk || Math.max(cre, mrsa));
            return {
                value: [col, row, maxVal],
                bedId: bedId,
                cre: cre,
                mrsa: mrsa
            };
        });

        this.riskData = heatData;
        this.chart.setOption({
            series: [{ data: heatData }]
        });
    };

    RiskHeatmap.prototype._bindResize = function () {
        var self = this;
        window.addEventListener('resize', function () {
            if (self.chart) {
                self.chart.resize();
            }
        });
    };

    RiskHeatmap.prototype.resize = function () {
        if (this.chart) {
            this.chart.resize();
        }
    };

    RiskHeatmap.prototype.dispose = function () {
        if (this.chart) {
            this.chart.dispose();
            this.chart = null;
        }
    };

    global.RiskHeatmap = RiskHeatmap;

})(window);
