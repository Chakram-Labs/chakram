export namespace appconfig {
	
	export class Config {
	    network: string;
	    minerAddress: string;
	    threads: number;
	    lightMode: boolean;
	    dataDir: string;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.network = source["network"];
	        this.minerAddress = source["minerAddress"];
	        this.threads = source["threads"];
	        this.lightMode = source["lightMode"];
	        this.dataDir = source["dataDir"];
	    }
	}

}

export namespace main {
	
	export class AppStatus {
	    processState: string;
	    plainStatus: string;
	    consensusHeight: number;
	    gethHeight: number;
	    gethPeers: number;
	    consensusPeers: number;
	    hashrate: number;
	    minerAddress: string;
	    balanceCHK: string;
	    blocksFound: number;
	    syncPct: number;
	    isSynced: boolean;
	    isMining: boolean;
	    lightMode: boolean;
	    threads: number;
	    networkHashrate: number;
	    updateAvailable: string;
	    version: string;
	    isConfigured: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AppStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.processState = source["processState"];
	        this.plainStatus = source["plainStatus"];
	        this.consensusHeight = source["consensusHeight"];
	        this.gethHeight = source["gethHeight"];
	        this.gethPeers = source["gethPeers"];
	        this.consensusPeers = source["consensusPeers"];
	        this.hashrate = source["hashrate"];
	        this.minerAddress = source["minerAddress"];
	        this.balanceCHK = source["balanceCHK"];
	        this.blocksFound = source["blocksFound"];
	        this.syncPct = source["syncPct"];
	        this.isSynced = source["isSynced"];
	        this.isMining = source["isMining"];
	        this.lightMode = source["lightMode"];
	        this.threads = source["threads"];
	        this.networkHashrate = source["networkHashrate"];
	        this.updateAvailable = source["updateAvailable"];
	        this.version = source["version"];
	        this.isConfigured = source["isConfigured"];
	    }
	}

}

export namespace node {
	
	export class GethBlock {
	    Number: number;
	    Hash: string;
	    Miner: string;
	    Timestamp: number;
	    Difficulty: string;
	    TxCount: number;
	
	    static createFrom(source: any = {}) {
	        return new GethBlock(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Number = source["Number"];
	        this.Hash = source["Hash"];
	        this.Miner = source["Miner"];
	        this.Timestamp = source["Timestamp"];
	        this.Difficulty = source["Difficulty"];
	        this.TxCount = source["TxCount"];
	    }
	}

}

