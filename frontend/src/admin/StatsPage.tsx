import { useEffect, useState } from 'react';
import { api } from '../api';
import type { SatisfactionStat } from '../types';
import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid, LineChart, Line, Legend } from 'recharts';

export default function StatsPage() {
  const [days, setDays] = useState(7);
  const [data, setData] = useState<SatisfactionStat | null>(null);
  const [err, setErr] = useState<string | null>(null);

  async function load() {
    try {
      const r = await api.statsSatisfaction(days);
      setData(r);
    } catch (e: any) {
      setErr(e?.response?.data?.message || '加载失败');
    }
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [days]);

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold text-slate-800">满意度统计</h2>
        <select value={days} onChange={(e) => setDays(Number(e.target.value))} className="border border-slate-200 rounded-md px-2 py-1 text-sm">
          {[7, 14, 30].map((d) => <option key={d} value={d}>最近 {d} 天</option>)}
        </select>
      </div>

      {err && <div className="text-rose-700 bg-rose-50 px-3 py-2 rounded-md text-sm mb-3">{err}</div>}

      {!data ? (
        <div className="text-slate-400">加载中…</div>
      ) : data.total === 0 ? (
        <div className="text-slate-400 bg-white border border-slate-200 rounded-xl p-12 text-center">
          还没有满意度评价数据
        </div>
      ) : (
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          <Card title="总评价数" value={data.total} />
          <Card title="平均分" value={data.average.toFixed(2)} suffix=" / 5" />
          <Card title="4-5 星占比" value={pct(data.distribution[3] + data.distribution[4], data.total)} suffix="%" />

          <div className="lg:col-span-2 bg-white border border-slate-200 rounded-xl p-4">
            <div className="text-sm text-slate-600 mb-2">评分分布</div>
            <div style={{ height: 240 }}>
              <ResponsiveContainer>
                <BarChart data={distData(data)}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#e2e8f0" />
                  <XAxis dataKey="label" />
                  <YAxis allowDecimals={false} />
                  <Tooltip />
                  <Bar dataKey="count" fill="#0284c7" radius={[4, 4, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            </div>
          </div>

          <div className="bg-white border border-slate-200 rounded-xl p-4">
            <div className="text-sm text-slate-600 mb-2">评分明细</div>
            <table className="w-full text-sm">
              <tbody>
                {data.distribution.map((c, i) => (
                  <tr key={i} className="border-t border-slate-100">
                    <td className="py-1">{'★'.repeat(i + 1)}</td>
                    <td className="py-1 text-right text-slate-500">{c}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="lg:col-span-3 bg-white border border-slate-200 rounded-xl p-4">
            <div className="text-sm text-slate-600 mb-2">每日评价趋势</div>
            <div style={{ height: 240 }}>
              <ResponsiveContainer>
                <LineChart data={data.byDay}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#e2e8f0" />
                  <XAxis dataKey="day" tickFormatter={(s) => s.slice(5)} />
                  <YAxis yAxisId="left" allowDecimals={false} />
                  <YAxis yAxisId="right" orientation="right" domain={[0, 5]} />
                  <Tooltip />
                  <Legend />
                  <Line yAxisId="left" type="monotone" dataKey="count" name="评价数" stroke="#0284c7" />
                  <Line yAxisId="right" type="monotone" dataKey="avg" name="平均分" stroke="#16a34a" />
                </LineChart>
              </ResponsiveContainer>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function Card({ title, value, suffix }: { title: string; value: number | string; suffix?: string }) {
  return (
    <div className="bg-white border border-slate-200 rounded-xl p-4">
      <div className="text-xs text-slate-500">{title}</div>
      <div className="text-3xl font-semibold text-slate-800 mt-1">
        {value}
        {suffix && <span className="text-base text-slate-400 ml-1">{suffix}</span>}
      </div>
    </div>
  );
}

function pct(num: number, total: number): number {
  if (!total) return 0;
  return Math.round((num / total) * 100);
}

function distData(d: SatisfactionStat) {
  return d.distribution.map((c, i) => ({ label: `${i + 1} 星`, count: c }));
}