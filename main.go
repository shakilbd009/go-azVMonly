package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/to"
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2019-12-01/compute"
	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2019-09-01/network"
	"github.com/Azure/go-autorest/autorest/azure/auth"
)

var (
	provider     = "az"
	region       = "eastus"
	avSku        = "aligned"
	RGname       = "az-nonProd-rg-001"
	VnetName     = "az-nonProd-vnet-001"
	subnetName   = "az-nonProd-sub-001"
	NSGname      = "az-nonProd-nsg-001"
	rdesc        = "az-nonProd-rule-001"
	subscription = ""
	username     = "usertest"
	passwd       = "useRword123$"
)

//OS publisher,offer
type OS struct {
	publisher string
	offer     string
}

type deployment struct {
	Env            string
	Tier           string
	Os             string
	Version        string
	Disks          string
	Count          string
	AppCode        string
	CRQ            string
	SubscriptionID string
}

func main() {
	now := time.Now()
	avch := make(chan string)
	sbch := make(chan string)
	nich := make(chan string)
	cmch := make(chan string)
	imch := make(chan *[]compute.VirtualMachineImageResource)
	dploy := new(deployment)
	if err := dploy.parseFlags(); err != nil {
		log.Fatalln(err)
	}
	//env, tierdploy.Os, version, dsks, countTo, appcode, crq, subscriptionID := parseFlags()
	AVsetname := fmt.Sprintf("%s-%s-avs-001", provider, dploy.Env)
	subscription = dploy.SubscriptionID
	ctx := context.Background()
	pubnoffer, err := getImagePubnOffer(dploy.Os)
	if err != nil {
		log.Fatalln(err)
	}
	go createAVS(ctx, AVsetname, RGname, avSku, region, avch)
	sknm, igvs := getImageVersion(ctx, pubnoffer, dploy.Os, dploy.Version, imch)
	if sknm == "" || igvs == "" {
		log.Fatalln("image version error")
	}

	vm := getVMname(dploy.Env, dploy.Os, dploy.AppCode)
	subnetname, err := getSubnetName(dploy.Tier, dploy.Env)
	if err != nil {
		log.Fatalln(err)
	}
	vname, err := getNetwork(dploy.Env)
	if err != nil {
		log.Fatalln(err)
	}
	go getSubnet(ctx, RGname, subnetname, vname, sbch)
	subnet := <-sbch
	avsnm := <-avch
	count := strings.Split(dploy.Count, "-")
	var wg sync.WaitGroup
	var mx sync.Mutex
	start, err := strconv.Atoi(count[0])
	if err != nil {
		log.Fatalln(err)
	}
	end, err := strconv.Atoi(count[1])
	if err != nil {
		log.Fatalln(err)
	}
	vmch := make(chan string, end-start)
	go func(vmch, ch chan string) {
		for i := start; i <= end; i++ {
			wg.Add(1)
			vmname := fmt.Sprintf("%s%02d", vm, i)
			nicname := fmt.Sprintf("%s-nic-01", vmname)
			disks := getDisks(&dploy.Disks, vmname)
			go func(vmname, nic string, disks *[]compute.DataDisk) {
				mx.Lock()
				go createNIC(ctx, RGname, nic, subscription, region, subnet, nich)
				go createVM(ctx, RGname, vmname, username, passwd, <-nich, avsnm, region, pubnoffer.publisher, pubnoffer.offer, sknm, igvs, &dploy.CRQ, disks, vmch)
				mx.Unlock()
				fmt.Printf("VM name: %s Deployed 🎉\n", <-vmch)
				wg.Done()
			}(vmname, nicname, &disks)
		}
		wg.Wait()
		close(vmch)
		ch <- "Done"
	}(vmch, cmch)

	for {
		select {
		case complete := <-cmch:
			fmt.Printf("deployment completed 🎉🎉🎉, %s\n", complete)
			done := time.Since(now)
			fmt.Printf("Time took: %.2f minutes", done.Minutes())
			return
		default:
			log.Println("Deploying VM...")
			time.Sleep(time.Millisecond * 1000)
		}
	}
}

func (d *deployment) parseFlags() error {

	flag.StringVar(&d.Env, "Env", "", "please provide environment name.")
	flag.StringVar(&d.Tier, "Tier", "", "please provdploy.Tier name. only app or web is allowed")
	flag.StringVar(&d.Os, "OS", "", "please provide OS name to be deployed")
	flag.StringVar(&d.Version, "version", "", "please provide OS version to be deployed")
	flag.StringVar(&d.Disks, "Disks", "", "please add disks to be added")
	flag.StringVar(&d.Count, "countTO", "", "Count the number of VMs to be deployed")
	flag.StringVar(&d.AppCode, "AppCode", "", "provide three letter app code for the deployment")
	flag.StringVar(&d.CRQ, "CRQ", "", "provide the CRQ # for the deployment")
	flag.StringVar(&d.SubscriptionID, "subscriptionID", "", "please provide subscriptionID")
	flag.Parse()
	if d.Disks == "" || d.Os == "" || d.SubscriptionID == "" || d.Env == "" || d.Count == "" || d.Tier == "" || d.Version == "" || d.AppCode == "" || d.CRQ == "" {
		fmt.Println("\nFollowing Flags are required to proceed")
		flag.PrintDefaults()
		return errors.New("Provide all the mandatory flags mentioned above")
	}
	return nil
}

//in-progress
func (d *deployment) toLower() {

	value := reflect.ValueOf(*d)
	for i := 0; i < value.NumField(); i++ {
		if v, ok := value.Field(i).Interface().(string); ok {
			v = strings.ToLower(v)
		}
	}

}

func getImageVersion(ctx context.Context, pubnoffer OS, os, version string, ch chan *[]compute.VirtualMachineImageResource) (string, string) {

	go getVMimages(ctx, region, pubnoffer.publisher, pubnoffer.offer, version, ch)
	versn := (*<-ch)
	return version, *versn[len(versn)-1].Name
}

func getSkus(ctx context.Context, region, publisher, offer string, ch chan *[]compute.VirtualMachineImageResource) {

	client := compute.NewVirtualMachineImagesClient(subscription)
	authorizer, err := auth.NewAuthorizerFromCLI()
	if err == nil {
		client.Authorizer = authorizer
	}
	defer errRecover()
	result, err := client.ListSkus(ctx, region, publisher, offer)
	if err != nil {
		panic(err)
	}
	ch <- result.Value
	close(ch)
}

func getVMname(envname, os, app string) string {

	env := strings.ToLower(envname)
	os = strings.ToLower(os)
	b := "b"
	d := "d"
	p := "p"
	w := "w"
	x := "x"
	s := "s"

	switch {

	case env == "base" && os == "windows":
		return fmt.Sprintf("az%s%sw%s%s", b, w, d, app)
	case env == "base" && os == "redhat":
		return fmt.Sprintf("az%s%sw%s%s", b, x, d, app)
	case env == "base" && os == "suse":
		return fmt.Sprintf("az%s%sw%s%s", b, x, d, app)
	case env == "prod" && os == "windows":
		return fmt.Sprintf("az%s%sw%s%s", p, w, p, app)
	case env == "prod" && os == "redhat":
		return fmt.Sprintf("az%s%sw%s%s", p, x, p, app)
	case env == "prod" && os == "suse":
		return fmt.Sprintf("az%s%sw%s%s", p, x, p, app)
	case env == "nonprod" && os == "windows":
		return fmt.Sprintf("az%s%sw%s%s", s, w, d, app)
	case env == "nonprod" && os == "redhat":
		return fmt.Sprintf("az%s%sw%s%s", s, x, d, app)
	case env == "nonprod" && os == "suse":
		return fmt.Sprintf("az%s%sw%s%s", s, x, d, app)
	}
	return ""
}

func getDisks(disklist *string, vmname string) []compute.DataDisk {

	sizes := strings.Split(*disklist, ",")
	disks := make([]compute.DataDisk, 0)
	for i, v := range sizes {
		size, err := strconv.Atoi(v)
		if err != nil {
			panic(err)
		}
		disks = append(disks, compute.DataDisk{
			Lun:          to.Int32Ptr(int32(i)),
			Name:         to.StringPtr(fmt.Sprintf("%s%02d", vmname, i+1)),
			DiskSizeGB:   to.Int32Ptr(int32(size)),
			Caching:      compute.CachingTypesReadWrite,
			CreateOption: compute.DiskCreateOptionTypesEmpty,
			ManagedDisk: &compute.ManagedDiskParameters{
				StorageAccountType: compute.StorageAccountTypesStandardLRS,
			},
		})
	}
	return disks
}

func getImagePubnOffer(name string) (os OS, e error) {

	windows := []string{"MicrosoftWindowsServer", "WindowsServer"}
	redhat := []string{"RedHat", "RHEL"}
	suse := []string{"SUSE", "SLES"}
	osname := strings.ToLower(name)
	switch {
	case osname == "windows":
		os.publisher = windows[0]
		os.offer = windows[1]
		return os, nil
	case osname == "redhat":
		os.publisher = redhat[0]
		os.offer = redhat[1]
		return os, nil
	case osname == "windows":
		os.publisher = suse[0]
		os.offer = suse[1]
		return os, nil
	}
	e = errors.New("only Windows, RedHat And Suse is allowed for OS")
	return
}

func getAVS(ctx context.Context, rg, name string) (string, error) {

	client := compute.NewAvailabilitySetsClient(subscription)
	authorizer, err := auth.NewAuthorizerFromCLI()
	if err == nil {
		client.Authorizer = authorizer
	}
	resp, err := client.Get(ctx, rg, name)
	if err != nil {
		return "", err
	}
	return *resp.Name, nil
}

func createNIC(ctx context.Context, rg, nicname, subscription, loc, subid string, ch chan string) {

	client := network.NewInterfacesClient(subscription)
	authorizer, err := auth.NewAuthorizerFromCLI()
	if err == nil {
		client.Authorizer = authorizer
	}
	defer errRecover()
	resp, err := client.CreateOrUpdate(ctx,
		rg,
		nicname,
		network.Interface{
			Location: to.StringPtr(loc),
			InterfacePropertiesFormat: &network.InterfacePropertiesFormat{
				IPConfigurations: &[]network.InterfaceIPConfiguration{
					{
						Name: to.StringPtr("ipConfig"),
						InterfaceIPConfigurationPropertiesFormat: &network.InterfaceIPConfigurationPropertiesFormat{
							PrivateIPAllocationMethod: network.Dynamic,
							PrivateIPAddressVersion:   network.IPv4,
							Subnet: &network.Subnet{
								ID: to.StringPtr(subid),
							},
						},
					},
				},
				EnableAcceleratedNetworking: to.BoolPtr(true),
			},
		},
	)
	inter, err := resp.Result(client)
	if err != nil {
		panic(err)
	}
	ch <- *inter.ID
}

func vmClient() compute.VirtualMachinesClient {

	vmClient := compute.NewVirtualMachinesClient(subscription)
	authorizer, err := auth.NewAuthorizerFromCLI()
	if err == nil {
		vmClient.Authorizer = authorizer
	}
	return vmClient
}

func createVM(ctx context.Context, rg, vmname, username, passwd, nic, avsID, region, publisher, offer, sku, version string, crq *string, datadisks *[]compute.DataDisk, ch chan string) {

	client := vmClient()
	defer errRecover()
	resp, err := client.CreateOrUpdate(ctx,
		rg,
		vmname,
		compute.VirtualMachine{
			Location: to.StringPtr(region),
			VirtualMachineProperties: &compute.VirtualMachineProperties{
				HardwareProfile: &compute.HardwareProfile{
					VMSize: compute.VirtualMachineSizeTypesStandardB1s,
				},
				StorageProfile: &compute.StorageProfile{
					OsDisk: &compute.OSDisk{
						Name:         to.StringPtr(fmt.Sprintf("%s-os", vmname)),
						Caching:      compute.CachingTypesReadWrite,
						CreateOption: compute.DiskCreateOptionTypesFromImage,
						ManagedDisk: &compute.ManagedDiskParameters{
							StorageAccountType: compute.StorageAccountTypesStandardLRS,
						},
					},
					ImageReference: &compute.ImageReference{
						Publisher: to.StringPtr(publisher),
						Offer:     to.StringPtr(offer),
						Sku:       to.StringPtr(sku),
						Version:   to.StringPtr(version),
					},
					DataDisks: datadisks,
				},
				OsProfile: &compute.OSProfile{
					ComputerName:  to.StringPtr(vmname),
					AdminUsername: to.StringPtr(username),
					AdminPassword: to.StringPtr(passwd),
				},
				NetworkProfile: &compute.NetworkProfile{
					NetworkInterfaces: &[]compute.NetworkInterfaceReference{
						{
							ID: to.StringPtr(nic),
						},
					},
				},
				AvailabilitySet: &compute.SubResource{
					ID: to.StringPtr(avsID),
				},
			},
			Tags: map[string]*string{"CRQ": crq},
		},
	)
	if err != nil {
		panic(err)
	}
	err = resp.WaitForCompletionRef(ctx, client.Client)
	if err != nil {
		panic(err)
	}
	inter, err := resp.Result(client)
	if err != nil {
		panic(err)
	}
	if err != nil {
		panic(err)
	}
	ch <- *inter.Name
}

func createAVS(ctx context.Context, name, rg, sku, loc string, ch chan string) {

	client := compute.NewAvailabilitySetsClient(subscription)
	authorizer, err := auth.NewAuthorizerFromCLI()
	if err == nil {
		client.Authorizer = authorizer
	}
	defer errRecover()
	avSet, err := client.CreateOrUpdate(ctx,
		rg,
		name,
		compute.AvailabilitySet{
			AvailabilitySetProperties: &compute.AvailabilitySetProperties{
				PlatformFaultDomainCount:  to.Int32Ptr(2),
				PlatformUpdateDomainCount: to.Int32Ptr(5),
			},
			Sku: &compute.Sku{
				Name: to.StringPtr(sku),
			},
			Name:     to.StringPtr(name),
			Location: to.StringPtr(loc),
		},
	)
	if err != nil {
		panic(err)
	}
	ch <- *avSet.ID
	close(ch)
}

func getVMimages(ctx context.Context, region, publisher, offer, skus string, ch chan *[]compute.VirtualMachineImageResource) {

	client := compute.NewVirtualMachineImagesClient(subscription)
	authorizer, err := auth.NewAuthorizerFromCLI()
	if err == nil {
		client.Authorizer = authorizer
	}
	defer errRecover()
	result, err := client.List(ctx, region, publisher, offer, skus, "", nil, "")
	if err != nil {
		panic(err)
	}
	ch <- result.Value
	close(ch)
}

func getSubnetName(tier, envname string) (string, error) {

	baseAppSub := []string{"az-base-sub-001", "az-base-app-sub-002"}
	ProdAppSub := []string{"az-Prod-sub-001", "az-Prod-app-sub-002"}
	nonProdAppSub := []string{"az-nonProd-sub-001", "az-nonProd-app-sub-002"}
	envname = strings.ToLower(envname)
	tiername := strings.ToLower(tier)
	switch {
	case envname == "nonprod" && tiername == "app":
		return nonProdAppSub[1], nil
	case envname == "nonprod" && tiername == "web":
		return nonProdAppSub[0], nil
	case envname == "prod" && tiername == "app":
		return ProdAppSub[1], nil
	case envname == "prod" && tiername == "web":
		return ProdAppSub[0], nil
	case envname == "base" && tiername == "app":
		return baseAppSub[1], nil
	case envname == "base" && tiername == "web":
		return baseAppSub[0], nil
	}
	err := errors.New("Please pass an env as prod, base or nonprod. And tier can only be app or web.")
	return "", err
}

func getNetwork(envname string) (string, error) {

	network := []string{"az-base-vnet-001", "az-nonProd-vnet-001", "az-Prod-vnet-001"}
	envname = strings.ToLower(envname)
	switch {
	case envname == "base":
		return network[0], nil
	case envname == "nonprod":
		return network[1], nil
	case envname == "prod":
		return network[2], nil
	}
	err := errors.New("provide a network envname, i.e base, dev, prod")
	return "", err
}

func getSubnet(ctx context.Context, rg, sname, vname string, ch chan string) {

	client := network.NewSubnetsClient(subscription)
	authorizer, err := auth.NewAuthorizerFromCLI()
	defer errRecover()
	if err == nil {
		client.Authorizer = authorizer
	}
	resp, err := client.Get(ctx, rg, vname, sname, "")
	if err != nil {
		panic(err)
	}
	ch <- *resp.ID
	close(ch)
}

func errRecover() {
	if r := recover(); r != nil {
		fmt.Println("An error has occured:")
		fmt.Printf("%s\n\n", strings.Repeat("💀", 20))
		fmt.Println(r)
		fmt.Printf("\n")
		fmt.Println(strings.Repeat("💀", 20))
		os.Exit(1) //optional, if you want to stop the excution if error occurs.
	}
}
