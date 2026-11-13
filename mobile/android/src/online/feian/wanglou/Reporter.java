package online.feian.wanglou;

import android.app.ActivityManager;
import android.app.job.JobInfo;
import android.app.job.JobScheduler;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.SharedPreferences;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.os.BatteryManager;
import android.os.Build;
import android.os.Environment;
import android.os.StatFs;
import android.os.SystemClock;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;
import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.Locale;
import java.util.TimeZone;

/**
 * 手机状态上报：电量、是否充电、网络、内存、存储。只发到构建时指定的天枢地址，
 * 只用一个仅能上报这台设备的采集器令牌。令牌存在应用私有存储里，网页读不回来。
 */
final class Reporter {
    static final int JOB_ID = 7001;
    static final int INTERVAL_S = 900;
    private static final String PREFS = "tianshu.reporter";

    private Reporter() {}

    static SharedPreferences prefs(Context c) {
        return c.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    static boolean configured(Context c) {
        return prefs(c).getString("token", null) != null;
    }

    static String device(Context c) {
        return prefs(c).getString("device", "");
    }

    /** 保存令牌并安排定时上报。系统最短间隔 15 分钟，省电模式下可能更久。 */
    static void configure(Context c, String token, String device) {
        prefs(c).edit().putString("token", token).putString("device", device).apply();
        schedule(c);
    }

    static void forget(Context c) {
        prefs(c).edit().remove("token").remove("device").apply();
        JobScheduler js = c.getSystemService(JobScheduler.class);
        if (js != null) js.cancel(JOB_ID);
    }

    static void schedule(Context c) {
        JobScheduler js = c.getSystemService(JobScheduler.class);
        if (js == null) return;
        JobInfo job = new JobInfo.Builder(JOB_ID, new ComponentName(c, ReportJob.class))
            .setRequiredNetworkType(JobInfo.NETWORK_TYPE_ANY)
            .setPeriodic(INTERVAL_S * 1000L)
            .setPersisted(true)
            .build();
        js.schedule(job);
    }

    /** 在后台线程调用。成功返回 null，否则返回原因。 */
    static String send(Context c, String hub) {
        SharedPreferences p = prefs(c);
        String token = p.getString("token", null);
        String device = p.getString("device", "");
        if (token == null || device.isEmpty()) return "未配置";
        try {
            String run = p.getString("run", null);
            if (run == null) {
                byte[] b = new byte[12];
                new SecureRandom().nextBytes(b);
                StringBuilder sb = new StringBuilder();
                for (byte x : b) sb.append(String.format("%02x", x));
                run = sb.toString();
                p.edit().putString("run", run).apply();
            }
            long seq = p.getLong("seq", 0) + 1;
            p.edit().putLong("seq", seq).apply();

            SimpleDateFormat iso = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.US);
            iso.setTimeZone(TimeZone.getTimeZone("UTC"));
            String now = iso.format(new Date());

            JSONObject host = new JSONObject();
            host.put("hostname", Build.MANUFACTURER + " " + Build.MODEL);
            host.put("os", "Android " + Build.VERSION.RELEASE);
            host.put("kernel", System.getProperty("os.version", ""));
            host.put("arch", Build.SUPPORTED_ABIS.length > 0 ? Build.SUPPORTED_ABIS[0] : "");
            host.put("cpu_count", Runtime.getRuntime().availableProcessors());
            host.put("uptime_s", SystemClock.elapsedRealtime() / 1000);

            JSONObject m = new JSONObject();
            Intent bat = c.registerReceiver(null, new IntentFilter(Intent.ACTION_BATTERY_CHANGED));
            if (bat != null) {
                int level = bat.getIntExtra(BatteryManager.EXTRA_LEVEL, -1);
                int scale = bat.getIntExtra(BatteryManager.EXTRA_SCALE, -1);
                if (level >= 0 && scale > 0) m.put("battery_pct", level * 100.0 / scale);
                int plugged = bat.getIntExtra(BatteryManager.EXTRA_PLUGGED, 0);
                m.put("charging", plugged != 0);
            }
            m.put("network", network(c));
            ActivityManager am = c.getSystemService(ActivityManager.class);
            if (am != null) {
                ActivityManager.MemoryInfo mi = new ActivityManager.MemoryInfo();
                am.getMemoryInfo(mi);
                m.put("mem_total", (double) mi.totalMem);
                m.put("mem_used", (double) (mi.totalMem - mi.availMem));
            }

            JSONArray disks = new JSONArray();
            StatFs fs = new StatFs(Environment.getDataDirectory().getPath());
            double total = fs.getTotalBytes(), free = fs.getAvailableBytes();
            if (total > 0) {
                disks.put(new JSONObject().put("mount", "/data").put("used", total - free).put("total", total));
            }

            JSONObject r = new JSONObject();
            r.put("device", device);
            r.put("collector", "phone");
            r.put("version", "android-0.3.0");
            r.put("run_id", run);
            r.put("seq", seq);
            r.put("sent_at", now);
            r.put("observed_at", now);
            r.put("interval_s", INTERVAL_S);
            r.put("host", host);
            r.put("metrics", m);
            r.put("disks", disks);
            r.put("units", new JSONArray());
            r.put("ports", new JSONArray());
            r.put("repos", new JSONArray());
            r.put("errors", new JSONArray());

            URL url = new URL(hub.replaceAll("/+$", "") + "/api/v1/ingest/report");
            HttpURLConnection conn = (HttpURLConnection) url.openConnection();
            conn.setConnectTimeout(15000);
            conn.setReadTimeout(20000);
            conn.setRequestMethod("POST");
            conn.setDoOutput(true);
            conn.setRequestProperty("Authorization", "Bearer " + token);
            conn.setRequestProperty("Content-Type", "application/json");
            byte[] body = r.toString().getBytes(StandardCharsets.UTF_8);
            try (OutputStream os = conn.getOutputStream()) {
                os.write(body);
            }
            int code = conn.getResponseCode();
            conn.disconnect();
            p.edit().putLong("last_try", System.currentTimeMillis()).putInt("last_code", code).apply();
            if (code == 401 || code == 403) {
                return "令牌已失效（" + code + "），请在天枢里重新开启上报";
            }
            if (code >= 300) return "天枢返回 " + code;
            p.edit().putLong("last_ok", System.currentTimeMillis()).apply();
            return null;
        } catch (Exception e) {
            return e.getClass().getSimpleName();
        }
    }

    private static String network(Context c) {
        ConnectivityManager cm = c.getSystemService(ConnectivityManager.class);
        if (cm == null) return "";
        Network n = cm.getActiveNetwork();
        NetworkCapabilities nc = n == null ? null : cm.getNetworkCapabilities(n);
        if (nc == null) return "offline";
        if (nc.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) return "wifi";
        if (nc.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) return "mobile";
        if (nc.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) return "ethernet";
        return "other";
    }
}
