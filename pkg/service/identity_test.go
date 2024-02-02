package service

import (
	"context"
	"fmt"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("IdentityService", func() {
	var (
		mockCtrl  *gomock.Controller
		underTest IdentityService
		mockProbe *fakeProber
	)

	BeforeEach(func() {
		mockProbe = &fakeProber{}
		mockCtrl = gomock.NewController(GinkgoT())
		underTest = IdentityService{connectivityProbe: mockProbe}
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	Describe("Get Plugin Info", func() {
		It("should get information", func() {
			res, err := underTest.GetPluginInfo(context.Background(), &csi.GetPluginInfoRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Name).To(Equal(VendorName))
			Expect(res.VendorVersion).To(Equal(VendorVersion))
		})
	})

	Describe("Get Plugin Capabilities", func() {
		It("should not fail", func() {
			res, err := underTest.GetPluginCapabilities(context.Background(), &csi.GetPluginCapabilitiesRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Capabilities).Should(Not(BeEmpty()))
		})
	})

	Describe("Call Probe", func() {
		var (
			err error
			res *csi.ProbeResponse
		)
		It("should fail when the probe fails", func() {
			mockProbe.err = fmt.Errorf("error")
			res, err = underTest.Probe(context.Background(), &csi.ProbeRequest{})
			Expect(err).To(HaveOccurred())
			Expect(res).To(BeNil())
		})
		It("should succeed when the probe succeeds", func() {
			mockProbe.err = nil
			res, err = underTest.Probe(context.Background(), &csi.ProbeRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.GetReady().Value).Should(BeTrue())
		})
	})
})
