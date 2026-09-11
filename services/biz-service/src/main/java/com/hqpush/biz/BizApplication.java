package com.hqpush.biz;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.scheduling.annotation.EnableScheduling;

/** biz-service 入口：REST /api/v1（auth、rules、alerts 补拉代理、quote 快照）。 */
@SpringBootApplication
@EnableScheduling // 到期降级等定时任务（docs/12 B19）
public class BizApplication {

    public static void main(String[] args) {
        SpringApplication.run(BizApplication.class, args);
    }
}
