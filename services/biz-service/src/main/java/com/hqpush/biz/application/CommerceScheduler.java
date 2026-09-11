package com.hqpush.biz.application;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.scheduling.annotation.Scheduled;
import org.springframework.stereotype.Component;

/** 商业化定时任务（docs/12 §2.2 B19 收尾）：到期套餐自动降级。
 *  每小时扫描一次过期非 free 套餐 → 降 free + 审计留痕。 */
@Component
public class CommerceScheduler {

    private static final Logger log = LoggerFactory.getLogger(CommerceScheduler.class);

    private final CommerceService commerce;

    public CommerceScheduler(CommerceService commerce) {
        this.commerce = commerce;
    }

    @Scheduled(cron = "0 7 * * * *") // 每小时第 7 分钟（避开整点高峰）
    public void expireDowngrade() {
        try {
            int n = commerce.expireOverdue();
            if (n > 0) {
                log.info("expire downgrade: {} users -> free", n);
            }
        } catch (Exception e) {
            log.error("expire downgrade failed", e);
        }
    }
}
