(function (global) {
    'use strict';

    function BedChart(containerId, options) {
        this.container = document.getElementById(containerId);
        if (!this.container) {
            console.error('BedChart: container not found:', containerId);
            return;
        }

        this.options = Object.assign({
            bedWidth: 70,
            bedHeight: 60,
            rows: 5,
            cols: 10
        }, options || {});

        this.canvas = document.createElement('canvas');
        this.canvas.id = 'bedLayoutCanvas';
        this.canvas.width = this.container.clientWidth || 1100;
        this.canvas.height = 550;
        this.ctx = this.canvas.getContext('2d');
        this.container.appendChild(this.canvas);

        this.beds = {};
        this.vitals = {};
        this.risks = {};
        this._onBedClick = null;

        this._bindEvents();
        this._startRenderLoop();
    }

    BedChart.prototype.setBeds = function (bedList) {
        var self = this;
        bedList.forEach(function (bed) {
            self.beds[bed.id] = bed;
        });
    };

    BedChart.prototype.setVitals = function (vitalsMap) {
        this.vitals = vitalsMap || {};
    };

    BedChart.prototype.setRisks = function (risksMap) {
        this.risks = risksMap || {};
    };

    BedChart.prototype.onBedClick = function (cb) {
        this._onBedClick = cb;
    };

    BedChart.prototype._bindEvents = function () {
        var self = this;
        this.canvas.addEventListener('click', function (e) {
            if (!self._onBedClick) return;
            var rect = self.canvas.getBoundingClientRect();
            var x = e.clientX - rect.left;
            var y = e.clientY - rect.top;

            for (var id in self.beds) {
                if (!self.beds.hasOwnProperty(id)) continue;
                var bed = self.beds[id];
                var bx = bed.location_x;
                var by = bed.location_y;
                if (x >= bx - 40 && x <= bx + 40 && y >= by - 40 && y <= by + 40) {
                    self._onBedClick(bed.id, bed);
                    break;
                }
            }
        });

        window.addEventListener('resize', function () {
            if (self.container) {
                self.canvas.width = self.container.clientWidth || 1100;
                self._draw();
            }
        });
    };

    BedChart.prototype._startRenderLoop = function () {
        var self = this;
        setInterval(function () {
            self._draw();
        }, 1000);
    };

    BedChart.prototype._draw = function () {
        var ctx = this.ctx;
        var canvas = this.canvas;
        ctx.clearRect(0, 0, canvas.width, canvas.height);
        this._drawGrid();
        this._drawWards();
        for (var id in this.beds) {
            if (this.beds.hasOwnProperty(id)) {
                this._drawSingleBed(this.beds[id]);
            }
        }
    };

    BedChart.prototype._drawGrid = function () {
        var ctx = this.ctx;
        var canvas = this.canvas;
        ctx.strokeStyle = 'rgba(59, 130, 246, 0.1)';
        ctx.lineWidth = 1;
        for (var x = 0; x < canvas.width; x += 50) {
            ctx.beginPath();
            ctx.moveTo(x, 0);
            ctx.lineTo(x, canvas.height);
            ctx.stroke();
        }
        for (var y = 0; y < canvas.height; y += 50) {
            ctx.beginPath();
            ctx.moveTo(0, y);
            ctx.lineTo(canvas.width, y);
            ctx.stroke();
        }
    };

    BedChart.prototype._drawWards = function () {
        var ctx = this.ctx;
        for (var row = 0; row < this.options.rows; row++) {
            ctx.fillStyle = 'rgba(59, 130, 246, 0.08)';
            ctx.fillRect(10, row * 100 + 10, 1060, 80);
            ctx.fillStyle = '#3b82f6';
            ctx.font = 'bold 12px sans-serif';
            ctx.fillText('病区 ' + (row + 1), 20, row * 100 + 30);
        }
    };

    BedChart.prototype._drawSingleBed = function (bed) {
        var ctx = this.ctx;
        var x = bed.location_x;
        var y = bed.location_y;
        var risk = this.risks[bed.id];
        var vitals = this.vitals[bed.id];

        var riskInfo = this._computeRiskLevel(risk);
        this._drawPulse(x, y, riskInfo);

        var bw = this.options.bedWidth;
        var bh = this.options.bedHeight;
        var bx = x - bw / 2;
        var by = y - bh / 2;

        ctx.fillStyle = riskInfo.level === 'critical' ? 'rgba(239, 68, 68, 0.25)'
            : riskInfo.level === 'warning' ? 'rgba(245, 158, 11, 0.25)'
            : 'rgba(30, 58, 100, 0.7)';
        ctx.strokeStyle = riskInfo.color;
        ctx.lineWidth = 2;
        this._roundRect(bx, by, bw, bh, 8);
        ctx.fill();
        ctx.stroke();

        ctx.fillStyle = '#fff';
        ctx.font = 'bold 13px sans-serif';
        ctx.textAlign = 'center';
        ctx.fillText(bed.bed_code, x, by + 20);

        if (vitals) {
            ctx.font = '10px sans-serif';
            ctx.fillStyle = '#f87171';
            ctx.fillText('\u2764 ' + (vitals.ecg ? vitals.ecg.toFixed(0) : '--'), x, by + 36);
            ctx.fillStyle = '#22c55e';
            ctx.fillText('\ud83e\ude78 ' + (vitals.spo2 ? vitals.spo2.toFixed(0) : '--') + '%', x, by + 48);
        }

        if (risk && risk.sofa_score >= 2) {
            ctx.fillStyle = '#ef4444';
            ctx.beginPath();
            ctx.arc(x + bw / 2 - 8, by - 6, 8, 0, Math.PI * 2);
            ctx.fill();
            ctx.fillStyle = '#fff';
            ctx.font = 'bold 9px sans-serif';
            ctx.fillText('!', x + bw / 2 - 8, by - 3);
        }
    };

    BedChart.prototype._computeRiskLevel = function (risk) {
        var level = 'normal';
        var color = '#22c55e';
        if (risk) {
            var maxRisk = Math.max(
                (risk.sofa_score || 0) / 12,
                risk.sepsis_probability || 0,
                risk.cre_risk || 0,
                risk.mrsa_risk || 0
            );
            if (maxRisk > 0.7 || risk.sofa_score >= 6) {
                level = 'critical';
                color = '#ef4444';
            } else if (maxRisk > 0.5 || risk.sofa_score >= 2) {
                level = 'warning';
                color = '#f59e0b';
            }
        }
        return { level: level, color: color };
    };

    BedChart.prototype._drawPulse = function (x, y, riskInfo) {
        if (riskInfo.level === 'normal') return;
        var ctx = this.ctx;
        var pulse = (Math.sin(Date.now() / 300) + 1) / 2;
        ctx.beginPath();
        ctx.arc(x, y, 45 + pulse * 8, 0, Math.PI * 2);
        ctx.fillStyle = riskInfo.color + '33';
        ctx.fill();
    };

    BedChart.prototype._roundRect = function (x, y, w, h, r) {
        var ctx = this.ctx;
        ctx.beginPath();
        ctx.moveTo(x + r, y);
        ctx.lineTo(x + w - r, y);
        ctx.quadraticCurveTo(x + w, y, x + w, y + r);
        ctx.lineTo(x + w, y + h - r);
        ctx.quadraticCurveTo(x + w, y + h, x + w - r, y + h);
        ctx.lineTo(x + r, y + h);
        ctx.quadraticCurveTo(x, y + h, x, y + h - r);
        ctx.lineTo(x, y + r);
        ctx.quadraticCurveTo(x, y, x + r, y);
        ctx.closePath();
    };

    global.BedChart = BedChart;

})(window);
