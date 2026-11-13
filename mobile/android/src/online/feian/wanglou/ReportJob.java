package online.feian.wanglou;

import android.app.job.JobParameters;
import android.app.job.JobService;

/** 系统定时唤起的上报任务。网络请求放到后台线程，失败就等下一轮，不重试、不排队。 */
public class ReportJob extends JobService {
    @Override
    public boolean onStartJob(final JobParameters params) {
        new Thread(new Runnable() {
            @Override
            public void run() {
                Reporter.send(getApplicationContext(), getString(R.string.hub_url));
                jobFinished(params, false);
            }
        }).start();
        return true;
    }

    @Override
    public boolean onStopJob(JobParameters params) {
        return false;
    }
}
