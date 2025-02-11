// Copyright 2022, Chef.  All rights reserved.
// https://github.com/q191201771/lal
//
// Use of this source code is governed by a MIT-style license
// that can be found in the License file.
//
// Author: Chef (191201771@qq.com)

package logic

import (
	"time"

	"github.com/q191201771/lal/pkg/base"
	"github.com/q191201771/lal/pkg/remux"
	"github.com/q191201771/lal/pkg/rtmp"
	"github.com/q191201771/lal/pkg/rtsp"
)

func (group *Group) AddRtmpPubSession(session *rtmp.ServerSession) error {
	group.mutex.Lock()
	defer group.mutex.Unlock()

	Log.Debugf("[%s] [%s] add rtmp pub session into group.", group.UniqueKey, session.UniqueKey())

	if group.hasInSession() {
		Log.Errorf("[%s] in stream already exist at group. add=%s, exist=%s",
			group.UniqueKey, session.UniqueKey(), group.inSessionUniqueKey())
		return base.ErrDupInStream
	}

	group.rtmpPubSession = session
	group.addIn()

	if group.shouldStartRtspRemuxer() {
		group.rtmp2RtspRemuxer = remux.NewRtmp2RtspRemuxer(
			group.onSdpFromRemux,
			group.onRtpPacketFromRemux,
		)
	}

	// TODO(chef): 为rtmp pull以及rtsp也添加叠加静音音频的功能
	if group.config.RtmpConfig.AddDummyAudioEnable {
		// TODO(chef): 从整体控制和锁关系来说，应该让pub的数据回调到group中进锁后再让数据流入filter
		group.dummyAudioFilter = remux.NewDummyAudioFilter(group.UniqueKey, group.config.RtmpConfig.AddDummyAudioWaitAudioMs, group.OnReadRtmpAvMsg)
		session.SetPubSessionObserver(group.dummyAudioFilter)
	} else {
		session.SetPubSessionObserver(group)
	}

	return nil
}

// AddRtspPubSession TODO chef: rtsp package中，增加回调返回值判断，如果是false，将连接关掉
func (group *Group) AddRtspPubSession(session *rtsp.PubSession) error {
	group.mutex.Lock()
	defer group.mutex.Unlock()

	Log.Debugf("[%s] [%s] add RTSP PubSession into group.", group.UniqueKey, session.UniqueKey())

	if group.hasInSession() {
		Log.Errorf("[%s] in stream already exist at group. wanna add=%s", group.UniqueKey, session.UniqueKey())
		return base.ErrDupInStream
	}

	group.rtspPubSession = session
	group.addIn()

	group.rtsp2RtmpRemuxer = remux.NewAvPacket2RtmpRemuxer(group.onRtmpMsgFromRemux)
	session.SetObserver(group)

	return nil
}

func (group *Group) AddRtmpPullSession(session *rtmp.PullSession) bool {
	group.mutex.Lock()
	defer group.mutex.Unlock()

	Log.Debugf("[%s] [%s] add PullSession into group.", group.UniqueKey, session.UniqueKey())

	if group.hasInSession() {
		Log.Errorf("[%s] in stream already exist. wanna add=%s", group.UniqueKey, session.UniqueKey())
		return false
	}

	group.pullProxy.pullSession = session
	group.addIn()

	if group.shouldStartRtspRemuxer() {
		group.rtmp2RtspRemuxer = remux.NewRtmp2RtspRemuxer(
			group.onSdpFromRemux,
			group.onRtpPacketFromRemux,
		)
	}

	return true
}

// ---------------------------------------------------------------------------------------------------------------------

func (group *Group) DelRtmpPubSession(session *rtmp.ServerSession) {
	group.mutex.Lock()
	defer group.mutex.Unlock()
	group.delRtmpPubSession(session)
}

func (group *Group) DelRtspPubSession(session *rtsp.PubSession) {
	group.mutex.Lock()
	defer group.mutex.Unlock()
	group.delRtspPubSession(session)
}

func (group *Group) DelRtmpPullSession(session *rtmp.PullSession) {
	group.mutex.Lock()
	defer group.mutex.Unlock()
	group.delRtmpPullSession(session)
}

// ---------------------------------------------------------------------------------------------------------------------

func (group *Group) delRtmpPubSession(session *rtmp.ServerSession) {
	Log.Debugf("[%s] [%s] del rtmp PubSession from group.", group.UniqueKey, session.UniqueKey())

	if session != group.rtmpPubSession {
		Log.Warnf("[%s] del rtmp pub session but not match. del session=%s, group session=%p",
			group.UniqueKey, session.UniqueKey(), group.rtmpPubSession)
		return
	}

	group.delIn()
}

func (group *Group) delRtspPubSession(session *rtsp.PubSession) {
	Log.Debugf("[%s] [%s] del rtsp PubSession from group.", group.UniqueKey, session.UniqueKey())

	if session != group.rtspPubSession {
		Log.Warnf("[%s] del rtmp pub session but not match. del session=%s, group session=%p",
			group.UniqueKey, session.UniqueKey(), group.rtspPubSession)
		return
	}

	group.delIn()
}

func (group *Group) delRtmpPullSession(session *rtmp.PullSession) {
	Log.Debugf("[%s] [%s] del rtmp PullSession from group.", group.UniqueKey, session.UniqueKey())

	group.pullProxy.pullSession = nil
	group.setPullingFlag(false)
	group.delIn()
}

// ---------------------------------------------------------------------------------------------------------------------

// addIn 有pub或pull的输入型session加入时，需要调用该函数
//
func (group *Group) addIn() {
	now := time.Now().Unix()

	if group.shouldStartMpegtsRemuxer() {
		group.rtmp2MpegtsRemuxer = remux.NewRtmp2MpegtsRemuxer(group)
	}

	group.startPushIfNeeded()
	group.startHlsIfNeeded()
	group.startRecordFlvIfNeeded(now)
	group.startRecordMpegtsIfNeeded(now)
}

// delIn 有pub或pull的输入型session离开时，需要调用该函数
//
func (group *Group) delIn() {
	// 注意，remuxer放前面，使得有机会将内部缓存的数据吐出来
	if group.rtmp2MpegtsRemuxer != nil {
		group.rtmp2MpegtsRemuxer.Dispose()
		group.rtmp2MpegtsRemuxer = nil
	}

	group.stopPushIfNeeded()
	group.stopHlsIfNeeded()
	group.stopRecordFlvIfNeeded()
	group.stopRecordMpegtsIfNeeded()

	group.rtmpPubSession = nil
	group.rtspPubSession = nil
	group.rtsp2RtmpRemuxer = nil
	group.rtmp2RtspRemuxer = nil
	group.dummyAudioFilter = nil

	group.rtmpGopCache.Clear()
	group.httpflvGopCache.Clear()
	group.httptsGopCache.Clear()
	group.sdpCtx = nil
	group.patpmt = nil
}
