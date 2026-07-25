package service

import (
	"fmt"
	"math"
	"strings"
	"time"

	"dengdeng/internal/model"

	"gorm.io/gorm"
)

// NotificationScheduler turns the notification switches into durable,
// deduplicated mail deliveries. A marker Setting prevents process restarts
// from sending the same reminder again on the same day.
type NotificationScheduler struct {
	db       *gorm.DB
	settings *SystemSettingsService
	mailer   OperationalAlertMailer
}

func NewNotificationScheduler(db *gorm.DB, settings *SystemSettingsService, mailer OperationalAlertMailer) *NotificationScheduler {
	return &NotificationScheduler{db: db, settings: settings, mailer: mailer}
}

func (s *NotificationScheduler) Start() {
	if s == nil || s.db == nil || s.settings == nil || s.mailer == nil {
		return
	}
	go func() {
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		for {
			<-timer.C
			s.run()
			timer.Reset(time.Hour)
		}
	}()
}

func (s *NotificationScheduler) run() {
	settings, err := s.settings.Get()
	if err != nil || !s.mailer.Configured() {
		return
	}
	if settings.Notifications.BalanceLowEnabled && settings.Notifications.BalanceLowThresholdMicro > 0 {
		s.sendBalanceReminders(settings.Notifications)
	}
	if settings.Notifications.SubscriptionExpiryEnabled {
		s.sendSubscriptionReminders()
	}
	if settings.Notifications.AccountQuotaEnabled && len(settings.Notifications.AccountQuotaEmails) > 0 {
		s.sendQuotaReminders(settings.Notifications.AccountQuotaEmails)
	}
}

func (s *NotificationScheduler) deliverOnce(kind string, id int64, recipient, title, summary string) {
	date := time.Now().UTC().Format("2006-01-02")
	key := fmt.Sprintf("notification.sent.v1.%s.%d.%s", kind, id, date)
	var count int64
	if s.db.Model(&model.Setting{}).Where("key = ?", key).Count(&count).Error != nil || count > 0 {
		return
	}
	if s.mailer.SendOperationalAlert(recipient, title, summary) != nil {
		return
	}
	_ = s.db.Create(&model.Setting{Key: key, Value: time.Now().UTC().Format(time.RFC3339)}).Error
}

func (s *NotificationScheduler) sendBalanceReminders(settings NotificationSettings) {
	var users []model.User
	if s.db.Where("status = ? AND balance_micro < ?", model.StatusActive, settings.BalanceLowThresholdMicro).Find(&users).Error != nil {
		return
	}
	for _, user := range users {
		summary := fmt.Sprintf("当前可用余额为 $%.6f，已低于提醒阈值 $%.6f。", float64(user.BalanceMicro)/1e6, float64(settings.BalanceLowThresholdMicro)/1e6)
		if url := strings.TrimSpace(settings.BalanceLowRechargeURL); url != "" {
			summary += "\n充值地址：" + url
		}
		s.deliverOnce("balance", user.ID, user.Email, "余额不足提醒", summary)
	}
}

func (s *NotificationScheduler) sendSubscriptionReminders() {
	now := time.Now().UTC()
	var subscriptions []model.UserGroupSubscription
	if s.db.Where("expires_at > ? AND expires_at <= ?", now, now.AddDate(0, 0, 7)).Find(&subscriptions).Error != nil {
		return
	}
	for _, subscription := range subscriptions {
		days := int(math.Ceil(subscription.ExpiresAt.Sub(now).Hours() / 24))
		if days != 7 && days != 3 && days != 1 {
			continue
		}
		var user model.User
		var group model.Group
		if s.db.First(&user, subscription.UserID).Error != nil || s.db.First(&group, subscription.GroupID).Error != nil {
			continue
		}
		kind := fmt.Sprintf("subscription-%d-%dd", subscription.ID, days)
		s.deliverOnce(kind, user.ID, user.Email, "订阅即将到期", fmt.Sprintf("分组「%s」的订阅将在 %d 天后到期。", group.Name, days))
	}
}

func (s *NotificationScheduler) sendQuotaReminders(recipients []string) {
	var snapshots []model.AccountQuotaSnapshot
	if s.db.Where("state IN ?", []string{"ready", "partial"}).Find(&snapshots).Error != nil {
		return
	}
	for _, snapshot := range snapshots {
		warning := ""
		for _, window := range snapshot.Windows {
			if window.UsedPercent != nil && *window.UsedPercent >= 90 {
				warning = fmt.Sprintf("%s 已使用 %.1f%%", window.Label, *window.UsedPercent)
				break
			}
			if window.Remaining != nil && *window.Remaining <= 0 {
				warning = fmt.Sprintf("%s 剩余额度已耗尽", window.Label)
				break
			}
		}
		if warning == "" {
			continue
		}
		for index, recipient := range recipients {
			id := snapshot.UpstreamAccountID*100 + int64(index)
			s.deliverOnce("upstream-quota", id, recipient, "上游账号额度提醒", fmt.Sprintf("账号 #%d（%s）：%s。", snapshot.UpstreamAccountID, snapshot.Platform, warning))
		}
	}
}
